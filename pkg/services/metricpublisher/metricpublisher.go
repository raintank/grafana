package metricpublisher

import (
	"encoding/json"
	"github.com/bitly/go-nsq"
	"github.com/grafana/grafana/pkg/log"
	met "github.com/grafana/grafana/pkg/metric"
	m "github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/setting"
)

var (
	globalProducer         *nsq.Producer
	topic                  string
	metricPublisherMetrics met.Count
	metricPublisherMsgs    met.Count
)

func Init(metrics met.Backend) {
	sec := setting.Cfg.Section("metric_publisher")

	if !sec.Key("enabled").MustBool(false) {
		return
	}

	addr := sec.Key("nsqd_addr").MustString("localhost:4150")
	topic = sec.Key("topic").MustString("metrics")
	cfg := nsq.NewConfig()
	var err error
	globalProducer, err = nsq.NewProducer(addr, cfg)
	if err != nil {
		log.Fatal(0, "failed to initialize nsq producer.", err)
	}
	metricPublisherMetrics = metrics.NewCount("metricpublisher.metrics-published")
	metricPublisherMsgs = metrics.NewCount("metricpublisher.messages-published")
}

func Publish(msgString []byte) {
	if globalProducer != nil {
		err := globalProducer.Publish(topic, msgString)
		if err != nil {
			log.Fatal(0, "failed to publish message to nsqd.", err)
		}
	}
}

func ProcessBuffer(c <-chan m.MetricDefinition) {
	for {
		select {
		case b := <-c:
			if b.OrgId != 0 {
				//get hash.
				msgString, err := json.Marshal(b)
				if err != nil {
					log.Error(0, "Failed to marshal metrics payload.", err)
				} else {
					Publish(msgString)
				}
			}
		}
	}
}
