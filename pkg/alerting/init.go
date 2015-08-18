package alerting

import (
	"fmt"
	"time"

	"github.com/grafana/grafana/pkg/log"
	met "github.com/grafana/grafana/pkg/metric"
	"github.com/grafana/grafana/pkg/services/rabbitmq"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/hashicorp/golang-lru"
)

var jobQueueInternalItems met.Gauge
var jobQueueInternalSize met.Gauge
var jobQueuePreAMQPItems met.Gauge
var jobQueuePreAMQPSize met.Gauge
var tickQueueItems met.Meter
var tickQueueSize met.Gauge
var dispatcherJobsSkippedDueToSlowJobQueueInternal met.Count
var dispatcherJobsSkippedDueToSlowJobQueuePreAMQP met.Count
var dispatcherTicksSkippedDueToSlowTickQueue met.Count

var dispatcherGetSchedules met.Timer
var dispatcherNumGetSchedules met.Count
var dispatcherJobSchedulesSeen met.Count
var dispatcherJobsScheduled met.Count

var executorNum met.Gauge
var executorConsiderJobAlreadyDone met.Timer
var executorConsiderJobOriginalTodo met.Timer

var executorNumAlreadyDone met.Count
var executorNumOriginalTodo met.Count
var executorAlertOutcomesErr met.Count
var executorAlertOutcomesOk met.Count
var executorAlertOutcomesWarn met.Count
var executorAlertOutcomesCrit met.Count
var executorAlertOutcomesUnkn met.Count
var executorGraphiteEmptyResponse met.Count
var executorGraphiteIncompleteResponse met.Count
var executorGraphiteBadStart met.Count
var executorGraphiteBadStep met.Count
var executorGraphiteBadSteps met.Count

var executorJobExecDelay met.Timer
var executorJobQueryGraphite met.Timer
var executorJobParseAndEval met.Timer
var executorGraphiteMissingVals met.Meter

// Init initalizes all metrics
// run this function when statsd is ready, so we can create the series
func Init(metrics met.Backend) {
	jobQueueInternalItems = metrics.NewGauge("alert-jobqueue-internal.items", 0)
	jobQueueInternalSize = metrics.NewGauge("alert-jobqueue-internal.size", int64(setting.InternalJobQueueSize))
	jobQueuePreAMQPItems = metrics.NewGauge("alert-jobqueue-preamqp.items", 0)
	jobQueuePreAMQPSize = metrics.NewGauge("alert-jobqueue-preamqp.size", int64(setting.PreAMQPJobQueueSize))
	tickQueueItems = metrics.NewMeter("alert-tickqueue.items", 0)
	tickQueueSize = metrics.NewGauge("alert-tickqueue.size", int64(setting.TickQueueSize))
	dispatcherJobsSkippedDueToSlowJobQueueInternal = metrics.NewCount("alert-dispatcher.jobs-skipped-due-to-slow-internal-jobqueue")
	dispatcherJobsSkippedDueToSlowJobQueuePreAMQP = metrics.NewCount("alert-dispatcher.jobs-skipped-due-to-slow-pre-amqp-jobqueue")
	dispatcherTicksSkippedDueToSlowTickQueue = metrics.NewCount("alert-dispatcher.ticks-skipped-due-to-slow-tickqueue")

	dispatcherGetSchedules = metrics.NewTimer("alert-dispatcher.get-schedules", 0)
	dispatcherNumGetSchedules = metrics.NewCount("alert-dispatcher.num-getschedules")
	dispatcherJobSchedulesSeen = metrics.NewCount("alert-dispatcher.job-schedules-seen")
	dispatcherJobsScheduled = metrics.NewCount("alert-dispatcher.jobs-scheduled")

	executorNum = metrics.NewGauge("alert-executor.num", 0)
	executorConsiderJobAlreadyDone = metrics.NewTimer("alert-executor.consider-job.already-done", 0)
	executorConsiderJobOriginalTodo = metrics.NewTimer("alert-executor.consider-job.original-todo", 0)

	executorNumAlreadyDone = metrics.NewCount("alert-executor.already-done")
	executorNumOriginalTodo = metrics.NewCount("alert-executor.original-todo")
	executorAlertOutcomesErr = metrics.NewCount("alert-executor.alert-outcomes.error")
	executorAlertOutcomesOk = metrics.NewCount("alert-executor.alert-outcomes.ok")
	executorAlertOutcomesWarn = metrics.NewCount("alert-executor.alert-outcomes.warning")
	executorAlertOutcomesCrit = metrics.NewCount("alert-executor.alert-outcomes.critical")
	executorAlertOutcomesUnkn = metrics.NewCount("alert-executor.alert-outcomes.unknown")
	executorGraphiteEmptyResponse = metrics.NewCount("alert-executor.graphite-emptyresponse")
	executorGraphiteIncompleteResponse = metrics.NewCount("alert-executor.graphite-incompleteresponse")
	executorGraphiteBadStart = metrics.NewCount("alert-executor.graphite-badstart")
	executorGraphiteBadStep = metrics.NewCount("alert-executor.graphite-badstep")
	executorGraphiteBadSteps = metrics.NewCount("alert-executor.graphite-badsteps")

	executorJobExecDelay = metrics.NewTimer("alert-executor.job_execution_delay", time.Duration(30)*time.Second)
	executorJobQueryGraphite = metrics.NewTimer("alert-executor.job_query_graphite", 0)
	executorJobParseAndEval = metrics.NewTimer("alert-executor.job_parse-and-evaluate", 0)
	executorGraphiteMissingVals = metrics.NewMeter("alert-executor.graphite-missingVals", 0)
}

func Construct() {
	cache, err := lru.New(setting.ExecutorLRUSize)
	if err != nil {
		panic(fmt.Sprintf("Can't create LRU: %s", err.Error()))
	}

	if setting.AlertingHandler != "amqp" && setting.AlertingHandler != "builtin" {
		log.Fatal(0, "alerting handler must be either 'builtin' or 'amqp'")
	}
	if setting.AlertingHandler == "amqp" {
		sec := setting.Cfg.Section("event_publisher")
		if !sec.Key("enabled").MustBool(false) {
			log.Fatal(0, "alerting handler 'amqp' requires the event_publisher to be enabled")
		}
		url := sec.Key("rabbitmq_url").String()
		if err := distributed(url, cache); err != nil {
			log.Fatal(0, "failed to start amqp consumer.", err)
		}
		return
	} else {
		if !setting.EnableScheduler {
			log.Fatal(0, "Alerting in standalone mode requires a scheduler (enable_scheduler = true)")
		}
		if setting.Executors == 0 {
			log.Fatal(0, "Alerting in standalone mode requires at least 1 executor (try: executors = 10)")
		}

		standalone(cache)
	}
}

func standalone(cache *lru.Cache) {
	jobQueue := newInternalJobQueue(setting.InternalJobQueueSize)

	// create jobs
	go Dispatcher(jobQueue)

	//start group of workers to execute the checks.
	for i := 0; i < setting.Executors; i++ {
		go ChanExecutor(GraphiteAuthContextReturner, jobQueue, cache)
	}
}

func distributed(url string, cache *lru.Cache) error {
	exchange := "alertingJobs"
	exch := rabbitmq.Exchange{
		Name:         exchange,
		ExchangeType: "x-consistent-hash",
		Durable:      true,
	}

	if setting.EnableScheduler {
		publisher := &rabbitmq.Publisher{Url: url, Exchange: &exch}
		err := publisher.Connect()
		if err != nil {
			return err
		}

		jobQueue := newPreAMQPJobQueue(setting.PreAMQPJobQueueSize, publisher)

		go Dispatcher(jobQueue)
	}

	q := rabbitmq.Queue{
		Name:       "",
		Durable:    false,
		AutoDelete: true,
		Exclusive:  true,
	}
	for i := 0; i < setting.Executors; i++ {
		consumer := rabbitmq.Consumer{
			Url:        url,
			Exchange:   &exch,
			Queue:      &q,
			BindingKey: []string{"10"}, //consistant hashing weight.
		}
		if err := consumer.Connect(); err != nil {
			log.Fatal(0, "failed to start event.consumer.", err)
		}
		AmqpExecutor(GraphiteAuthContextReturner, consumer, cache)
	}
	return nil
}
