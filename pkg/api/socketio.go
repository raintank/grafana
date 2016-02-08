package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/googollee/go-socket.io"
	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/components/apikeygen"
	"github.com/grafana/grafana/pkg/events"
	"github.com/grafana/grafana/pkg/log"
	"github.com/grafana/grafana/pkg/middleware"
	m "github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/collectoreventpublisher"
	"github.com/grafana/grafana/pkg/services/metricpublisher"
	"github.com/grafana/grafana/pkg/services/rabbitmq"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/raintank/met"
	"github.com/raintank/raintank-metric/schema"
	"github.com/streadway/amqp"
)

var server *socketio.Server
var contextCache *ContextCache
var metricsRecvd met.Count

type ContextCache struct {
	sync.RWMutex
	Contexts      map[string]*CollectorContext
	monitorsIndex map[int64]*m.MonitorDTO // index of monitors by Id
}

func (s *ContextCache) Set(id string, context *CollectorContext) {
	s.Lock()
	defer s.Unlock()
	s.Contexts[id] = context
	if s.monitorsIndex == nil {
		s.loadMonitors()
	}
	newIndex := make(map[int64]*m.MonitorDTO)
	for _, mon := range s.monitorsIndex {
		for _, col := range mon.Collectors {
			if col == context.Collector.Id {
				newIndex[mon.Id] = mon
				break
			}
		}
	}
	context.MonitorsIndex = newIndex
}

func (s *ContextCache) Remove(id string) {
	s.Lock()
	defer s.Unlock()
	delete(s.Contexts, id)
}

func (s *ContextCache) Emit(id string, event string, payload interface{}) {
	s.RLock()
	defer s.RUnlock()
	context, ok := s.Contexts[id]
	if !ok {
		log.Info("socket " + id + " is not local.")
		return
	}
	context.Socket.Emit(event, payload)
}

func (c *ContextCache) Refresh(collectorId int64) {
	c.RLock()
	defer c.RUnlock()

	for _, ctx := range c.Contexts {
		if ctx.Collector.Id == collectorId {
			ctx.Refresh()
		}
	}
}

func (c *ContextCache) loadMonitors() {
	monQuery := m.GetMonitorsQuery{
		IsGrafanaAdmin: true,
		Enabled:        "true",
	}
	newIndex := make(map[int64]*m.MonitorDTO)
	newCollectorIndex := make(map[int64]map[int64]*m.MonitorDTO)

	if err := bus.Dispatch(&monQuery); err != nil {
		log.Error(0, "failed to get list of monitors.", err)
	} else {
		for _, mon := range monQuery.Result {
			newIndex[mon.Id] = mon
			for _, col := range mon.Collectors {
				if _, ok := newCollectorIndex[col]; !ok {
					newCollectorIndex[col] = make(map[int64]*m.MonitorDTO)
				}
				newCollectorIndex[col][mon.Id] = mon
			}
		}
		c.monitorsIndex = newIndex
	}
	for _, ctx := range c.Contexts {
		cIdx, ok := newCollectorIndex[ctx.Collector.Id]
		if !ok {
			cIdx = make(map[int64]*m.MonitorDTO)
		}
		ctx.MonitorsIndex = cIdx
	}
}

func (c *ContextCache) AddMonitor(event *events.MonitorCreated) {
	mon := &m.MonitorDTO{
		Id:            event.Id,
		OrgId:         event.OrgId,
		EndpointId:    event.EndpointId,
		EndpointSlug:  event.EndpointSlug,
		MonitorTypeId: event.MonitorTypeId,
		CollectorIds:  event.CollectorIds,
		CollectorTags: event.CollectorTags,
		Collectors:    event.Collectors,
		Settings:      event.Settings,
		Frequency:     event.Frequency,
		Offset:        event.Offset,
		Enabled:       event.Enabled,
		Updated:       event.Updated,
	}
	// get map of collectors.
	colList := make(map[int64]bool)
	for _, col := range mon.Collectors {
		colList[col] = true
	}
	c.Lock()
	defer c.Unlock()
	// add the monitor to the full index.
	c.monitorsIndex[event.Id] = mon

	// add the monitor to the perCollector index, for each collector it is enabled on.
	for _, ctx := range c.Contexts {
		if _, ok := colList[ctx.Collector.Id]; ok {
			ctx.MonitorsIndex[event.Id] = mon
		}
	}
}

func (c *ContextCache) UpdateMonitor(event *events.MonitorUpdated) {
	mon := &m.MonitorDTO{
		Id:            event.Id,
		OrgId:         event.OrgId,
		EndpointId:    event.EndpointId,
		EndpointSlug:  event.EndpointSlug,
		MonitorTypeId: event.MonitorTypeId,
		CollectorIds:  event.CollectorIds,
		CollectorTags: event.CollectorTags,
		Collectors:    event.Collectors,
		Settings:      event.Settings,
		Frequency:     event.Frequency,
		Offset:        event.Offset,
		Enabled:       event.Enabled,
		Updated:       event.Updated,
	}
	// get map of collectors.
	colList := make(map[int64]bool)
	for _, col := range mon.Collectors {
		colList[col] = true
	}
	c.Lock()
	defer c.Unlock()
	c.monitorsIndex[event.Id] = mon
	// add the monitor to the perCollector index, for each collector it is enabled on.
	for _, ctx := range c.Contexts {
		if _, ok := colList[ctx.Collector.Id]; ok {
			ctx.MonitorsIndex[event.Id] = mon
		}
	}

	// remove monitor from collectors it is no longer on.
	toDelete := make(map[int64]bool)
	for _, collectorId := range event.LastState.Collectors {
		if _, ok := colList[collectorId]; !ok {
			toDelete[collectorId] = true
		}
	}
	for _, ctx := range c.Contexts {
		if _, ok := toDelete[ctx.Collector.Id]; ok {
			delete(ctx.MonitorsIndex, event.Id)
		}
	}
}

func (c *ContextCache) DeleteMonitor(event *events.MonitorRemoved) {
	c.Lock()
	defer c.Unlock()
	delete(c.monitorsIndex, event.Id)

	for _, ctx := range c.Contexts {
		delete(ctx.MonitorsIndex, event.Id)
	}
}

func (c *ContextCache) RefreshLoop() {
	ticker := time.NewTicker(time.Minute * 5)
	for {
		select {
		case <-ticker.C:
			c.RLock()
			c.loadMonitors()
			for _, ctx := range c.Contexts {
				ctx.Refresh()
			}
			c.RUnlock()
		}
	}
}

func NewContextCache() *ContextCache {
	cache := &ContextCache{
		Contexts: make(map[string]*CollectorContext),
	}

	go cache.RefreshLoop()
	return cache
}

type CollectorContext struct {
	*m.SignedInUser
	Collector     *m.CollectorDTO
	Socket        socketio.Socket
	SocketId      string
	MonitorsIndex map[int64]*m.MonitorDTO // index of monitors by Id
}

type BinMessage struct {
	Payload *socketio.Attachment
}

func register(so socketio.Socket) (*CollectorContext, error) {
	req := so.Request()
	req.ParseForm()
	keyString := req.Form.Get("apiKey")
	name := req.Form.Get("name")
	if name == "" {
		return nil, errors.New("collector name not provided.")
	}

	lastSocketId := req.Form.Get("lastSocketId")

	versionStr := req.Form.Get("version")
	if versionStr == "" {
		return nil, errors.New("version number not provided.")
	}
	versionParts := strings.SplitN(versionStr, ".", 2)
	if len(versionParts) != 2 {
		return nil, errors.New("could not parse version number")
	}
	versionMajor, err := strconv.ParseInt(versionParts[0], 10, 64)
	if err != nil {
		return nil, errors.New("could not parse version number")
	}
	versionMinor, err := strconv.ParseFloat(versionParts[1], 64)
	if err != nil {
		return nil, errors.New("could not parse version number.")
	}

	//--------- set required version of collector.------------//
	//
	if versionMajor < 0 || versionMinor < 1.3 {
		return nil, errors.New("invalid collector version. Please upgrade.")
	}
	//
	//--------- set required version of collector.------------//
	log.Info("collector %s with version %d.%f connected", name, versionMajor, versionMinor)
	if keyString != "" {
		// base64 decode key
		decoded, err := apikeygen.Decode(keyString)
		if err != nil {
			return nil, m.ErrInvalidApiKey
		}
		// fetch key
		keyQuery := m.GetApiKeyByNameQuery{KeyName: decoded.Name, OrgId: decoded.OrgId}
		if err := bus.Dispatch(&keyQuery); err != nil {
			return nil, m.ErrInvalidApiKey
		}
		apikey := keyQuery.Result

		// validate api key
		if !apikeygen.IsValid(decoded, apikey.Key) {
			return nil, m.ErrInvalidApiKey
		}
		// lookup collector
		colQuery := m.GetCollectorByNameQuery{Name: name, OrgId: apikey.OrgId}
		if err := bus.Dispatch(&colQuery); err != nil {
			//collector not found, so lets create a new one.
			colCmd := m.AddCollectorCommand{
				OrgId:   apikey.OrgId,
				Name:    name,
				Enabled: true,
			}
			if err := bus.Dispatch(&colCmd); err != nil {
				return nil, err
			}
			colQuery.Result = colCmd.Result
		}

		sess := &CollectorContext{
			SignedInUser: &m.SignedInUser{
				IsGrafanaAdmin: apikey.IsAdmin,
				OrgRole:        apikey.Role,
				ApiKeyId:       apikey.Id,
				OrgId:          apikey.OrgId,
				Name:           apikey.Name,
			},
			Collector: colQuery.Result,
			Socket:    so,
			SocketId:  so.Id(),
		}
		log.Info("collector %s with id %d owned by %d authenticated successfully.", name, colQuery.Result.Id, apikey.OrgId)
		if lastSocketId != "" {
			log.Info("removing socket with Id %s", lastSocketId)
			cmd := &m.DeleteCollectorSessionCommand{
				SocketId:    lastSocketId,
				OrgId:       sess.OrgId,
				CollectorId: sess.Collector.Id,
			}
			if err := bus.Dispatch(cmd); err != nil {
				log.Error(0, "failed to remove collectors lastSocketId", err)
				return nil, err
			}
		}

		if err := sess.Save(); err != nil {
			return nil, err
		}
		log.Info("saving session to contextCache")
		contextCache.Set(sess.SocketId, sess)
		log.Info("session saved to contextCache")
		return sess, nil
	}
	return nil, m.ErrInvalidApiKey
}

func InitCollectorController(metrics met.Backend) {
	sec := setting.Cfg.Section("event_publisher")
	cmd := &m.ClearCollectorSessionCommand{
		InstanceId: setting.InstanceId,
	}
	if err := bus.Dispatch(cmd); err != nil {
		log.Fatal(0, "failed to clear collectorSessions", err)
	}

	if sec.Key("enabled").MustBool(false) {
		url := sec.Key("rabbitmq_url").String()
		exchange := sec.Key("exchange").String()
		exch := rabbitmq.Exchange{
			Name:         exchange,
			ExchangeType: "topic",
			Durable:      true,
		}
		q := rabbitmq.Queue{
			Name:       "",
			Durable:    false,
			AutoDelete: true,
			Exclusive:  true,
		}
		consumer := rabbitmq.Consumer{
			Url:        url,
			Exchange:   &exch,
			Queue:      &q,
			BindingKey: []string{"INFO.monitor.*", "INFO.collector.*"},
		}
		err := consumer.Connect()
		if err != nil {
			log.Fatal(0, "failed to start event.consumer.", err)
		}
		consumer.Consume(eventConsumer)
	} else {
		//tap into the update/add/Delete events emitted when monitors are modified.
		bus.AddEventListener(EmitUpdateMonitor)
		bus.AddEventListener(EmitAddMonitor)
		bus.AddEventListener(EmitDeleteMonitor)
		bus.AddEventListener(HandleCollectorConnected)
		bus.AddEventListener(HandleCollectorDisconnected)
	}
	metricsRecvd = metrics.NewCount("collector-ctrl.metrics-recv")
}

func init() {
	contextCache = NewContextCache()
	var err error
	server, err = socketio.NewServer([]string{"polling", "websocket"})
	if err != nil {
		log.Fatal(4, "failed to initialize socketio.", err)
		return
	}
	server.On("connection", func(so socketio.Socket) {
		c, err := register(so)
		if err != nil {
			if err == m.ErrInvalidApiKey {
				log.Info("collector failed to authenticate.")
			} else if err.Error() == "invalid collector version. Please upgrade." {
				log.Info("collector is wrong version")
			} else {
				log.Error(0, "Failed to initialize collector.", err)
			}
			so.Emit("error", err.Error())
			return
		}
		log.Info("connection registered without error")
		if err := c.EmitReady(); err != nil {
			return
		}
		log.Info("binding event handlers for collector %s owned by OrgId: %d", c.Collector.Name, c.OrgId)
		c.Socket.On("event", c.OnEvent)
		c.Socket.On("results", c.OnResults)
		c.Socket.On("disconnection", c.OnDisconnection)

		log.Info("calling refresh for collector %s owned by OrgId: %d", c.Collector.Name, c.OrgId)
		time.Sleep(time.Second)
		c.Refresh()
	})

	server.On("error", func(so socketio.Socket, err error) {
		log.Error(0, "socket emitted error", err)
	})

}

func (c *CollectorContext) EmitReady() error {
	//get list of monitorTypes
	cmd := &m.GetMonitorTypesQuery{}
	if err := bus.Dispatch(cmd); err != nil {
		log.Error(0, "Failed to initialize collector.", err)
		c.Socket.Emit("error", err)
		return err
	}
	log.Info("sending ready event to collector %s", c.Collector.Name)
	readyPayload := map[string]interface{}{
		"collector":     c.Collector,
		"monitor_types": cmd.Result,
		"socket_id":     c.SocketId,
	}
	c.Socket.Emit("ready", readyPayload)
	return nil
}

func (c *CollectorContext) Save() error {
	cmd := &m.AddCollectorSessionCommand{
		CollectorId: c.Collector.Id,
		SocketId:    c.Socket.Id(),
		OrgId:       c.OrgId,
		InstanceId:  setting.InstanceId,
	}
	if err := bus.Dispatch(cmd); err != nil {
		log.Info("could not write collector_sesison to DB.", err)
		return err
	}
	log.Info("collector_session %s for collector_id: %d saved to DB.", cmd.SocketId, cmd.CollectorId)
	return nil
}

func (c *CollectorContext) Update() error {
	cmd := &m.UpdateCollectorSessionCmd{
		CollectorId: c.Collector.Id,
		SocketId:    c.Socket.Id(),
		OrgId:       c.OrgId,
	}
	if err := bus.Dispatch(cmd); err != nil {
		return err
	}
	return nil
}

func (c *CollectorContext) Remove() error {
	log.Info(fmt.Sprintf("removing socket with Id %s", c.SocketId))
	cmd := &m.DeleteCollectorSessionCommand{
		SocketId:    c.SocketId,
		OrgId:       c.OrgId,
		CollectorId: c.Collector.Id,
	}
	err := bus.Dispatch(cmd)
	return err
}

func (c *CollectorContext) OnDisconnection() {
	log.Info(fmt.Sprintf("%s disconnected", c.Collector.Name))
	if err := c.Remove(); err != nil {
		log.Error(4, fmt.Sprintf("Failed to remove collectorSession. %s", c.Collector.Name), err)
	}
	contextCache.Remove(c.SocketId)
}

func (c *CollectorContext) OnEvent(msg *schema.ProbeEvent) {
	log.Info(fmt.Sprintf("received event from %s", c.Collector.Name))
	if !c.Collector.Public {
		msg.OrgId = c.OrgId
	}

	if err := collectoreventpublisher.Publish(msg); err != nil {
		log.Error(0, "failed to publish event.", err)
	}
}

func (c *CollectorContext) OnResults(results []*schema.MetricData) {
	metricsRecvd.Inc(int64(len(results)))
	for _, r := range results {
		r.SetId()
		if !c.Collector.Public {
			r.OrgId = int(c.OrgId)
		}
	}
	metricpublisher.Publish(results)
}

func (c *CollectorContext) Refresh() {
	log.Info("Collector %d refreshing", c.Collector.Id)
	//step 1. get list of collectorSessions for this collector.
	q := m.GetCollectorSessionsQuery{CollectorId: c.Collector.Id}
	if err := bus.Dispatch(&q); err != nil {
		log.Error(0, "failed to get list of collectorSessions.", err)
		return
	}
	org := c.Collector.OrgId
	if c.Collector.Public {
		org = 0
	}
	totalSessions := int64(len(q.Result))
	//step 2. for each session
	for pos, sess := range q.Result {
		//we only need to refresh the 1 socket.
		if sess.SocketId != c.SocketId {
			continue
		}
		//step 3. get list of monitors configured for this colletor.

		log.Info("sending refresh to " + sess.SocketId)
		//step 5. send to socket.
		monitors := make([]*m.MonitorDTO, 0, len(c.MonitorsIndex))
		for _, mon := range c.MonitorsIndex {
			if (org == 0 || mon.OrgId == org) && (mon.Id%totalSessions == int64(pos)) {
				monitors = append(monitors, mon)
			}
		}
		c.Socket.Emit("refresh", monitors)
	}
}

func SocketIO(c *middleware.Context) {
	if server == nil {
		log.Fatal(4, "socket.io server not initialized.", nil)
	}

	server.ServeHTTP(c.Resp, c.Req.Request)
}

func EmitUpdateMonitor(event *events.MonitorUpdated) error {
	log.Info("processing monitorUpdated event.")
	contextCache.UpdateMonitor(event)
	seenCollectors := make(map[int64]bool)
	for _, collectorId := range event.Collectors {
		seenCollectors[collectorId] = true
		if err := EmitEvent(collectorId, "updated", event); err != nil {
			return err
		}
	}
	for _, collectorId := range event.LastState.Collectors {
		if _, ok := seenCollectors[collectorId]; !ok {
			if err := EmitEvent(collectorId, "removed", event); err != nil {
				return err
			}

		}
	}

	return nil
}

func EmitAddMonitor(event *events.MonitorCreated) error {
	log.Info("processing monitorCreated event")
	contextCache.AddMonitor(event)
	for _, collectorId := range event.Collectors {
		if err := EmitEvent(collectorId, "created", event); err != nil {
			return err
		}
	}
	return nil
}

func EmitDeleteMonitor(event *events.MonitorRemoved) error {
	log.Info("processing monitorRemoved event")
	contextCache.DeleteMonitor(event)
	for _, collectorId := range event.Collectors {
		if err := EmitEvent(collectorId, "removed", event); err != nil {
			return err
		}
	}
	return nil
}

func EmitEvent(collectorId int64, eventName string, event interface{}) error {
	q := m.GetCollectorSessionsQuery{CollectorId: collectorId}
	if err := bus.Dispatch(&q); err != nil {
		return err
	}
	totalSessions := int64(len(q.Result))
	if totalSessions < 1 {
		return nil
	}
	eventId := reflect.ValueOf(event).Elem().FieldByName("Id").Int()
	log.Info(fmt.Sprintf("emitting %s event for MonitorId %d totalSessions: %d", eventName, eventId, totalSessions))
	pos := eventId % totalSessions
	if q.Result[pos].InstanceId == setting.InstanceId {
		socketId := q.Result[pos].SocketId
		contextCache.Emit(socketId, eventName, event)
	}
	return nil
}

func HandleCollectorConnected(event *events.CollectorConnected) error {
	contextCache.Refresh(event.CollectorId)
	return nil
}

func HandleCollectorDisconnected(event *events.CollectorDisconnected) error {
	contextCache.Refresh(event.CollectorId)
	return nil
}

func HandleCollectorUpdated(event *events.CollectorUpdated) error {
	contextCache.RLock()
	defer contextCache.RUnlock()
	// get list of local sockets for this collector.
	contexts := make([]*CollectorContext, 0)
	for _, ctx := range contextCache.Contexts {
		if ctx.Collector.Id == event.Id {
			contexts = append(contexts, ctx)
		}
	}
	if len(contexts) > 0 {
		q := m.GetCollectorByIdQuery{
			Id:    event.Id,
			OrgId: contexts[0].Collector.OrgId,
		}
		if err := bus.Dispatch(&q); err != nil {
			return err
		}
		for _, ctx := range contexts {
			ctx.Collector = q.Result
			_ = ctx.EmitReady()
		}
	}

	return nil
}

func eventConsumer(msg *amqp.Delivery) error {
	log.Info("processing amqp message with routing key: " + msg.RoutingKey)
	eventRaw := events.OnTheWireEvent{}
	err := json.Unmarshal(msg.Body, &eventRaw)
	if err != nil {
		log.Error(0, "failed to unmarshal event.", err)
	}
	payloadRaw, err := json.Marshal(eventRaw.Payload)
	if err != nil {
		log.Error(0, "unable to marshal event payload back to json.", err)
	}
	switch msg.RoutingKey {
	case "INFO.monitor.updated":
		event := events.MonitorUpdated{}
		if err := json.Unmarshal(payloadRaw, &event); err != nil {
			log.Error(0, "unable to unmarshal payload into MonitorUpdated event.", err)
			return err
		}
		if err := EmitUpdateMonitor(&event); err != nil {
			return err
		}
		break
	case "INFO.monitor.created":
		event := events.MonitorCreated{}
		if err := json.Unmarshal(payloadRaw, &event); err != nil {
			log.Error(0, "unable to unmarshal payload into MonitorUpdated event.", err)
			return err
		}
		if err := EmitAddMonitor(&event); err != nil {
			return err
		}
		break
	case "INFO.monitor.removed":
		event := events.MonitorRemoved{}
		if err := json.Unmarshal(payloadRaw, &event); err != nil {
			log.Error(0, "unable to unmarshal payload into MonitorUpdated event.", err)
			return err
		}
		if err := EmitDeleteMonitor(&event); err != nil {
			return err
		}
		break
	case "INFO.collector.connected":
		event := events.CollectorConnected{}
		if err := json.Unmarshal(payloadRaw, &event); err != nil {
			log.Error(0, "unable to unmarshal payload into CollectorConnected event.", err)
			return err
		}
		if err := HandleCollectorConnected(&event); err != nil {
			return err
		}
		break
	case "INFO.collector.disconnected":
		event := events.CollectorDisconnected{}
		if err := json.Unmarshal(payloadRaw, &event); err != nil {
			log.Error(0, "unable to unmarshal payload into CollectorDisconnected event.", err)
			return err
		}
		if err := HandleCollectorDisconnected(&event); err != nil {
			return err
		}
		break
	case "INFO.collector.updated":
		event := events.CollectorUpdated{}
		if err := json.Unmarshal(payloadRaw, &event); err != nil {
			log.Error(0, "unable to unmarshal payload into CollectorUpdated event.", err)
			return err
		}
		if err := HandleCollectorUpdated(&event); err != nil {
			return err
		}
		break
	}

	return nil
}
