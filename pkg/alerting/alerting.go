package alerting

import (
	"bosun.org/cmd/bosun/cache"
	"bosun.org/cmd/bosun/expr"
	"bosun.org/cmd/bosun/sched"
	"bosun.org/graphite"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"time"
)

// getAlignedTicker returns a ticker that ticks at the next second or very shortly after
func getAlignedTicker() *time.Ticker {
	unix := time.Now().UnixNano()
	diff := time.Duration(time.Second - (time.Duration(unix) % time.Second))
	return time.NewTicker(diff)
}

type Job struct {
	key  string
	expr string
	ts   time.Time
}

var queue = make(chan Job) // TODO: use rabbitmq or something so we can have multiple grafana dispatchers and executors

// Dispatcher dispatches, every second, all jobs that should run for that second
// every job has an id so that you can run multiple dispatchers (for HA) while still only processing each job once.
// (provided jobs get consistently routed to executors)
func Dispatcher() {
	for {
		ticker := getAlignedTicker()
		select {
		case t := <-ticker.C:
			go dispatchJobs(t)
		}
	}
}

func dispatchJobs(t time.Time) {
	normalizedTime := t.Unix()
	fmt.Println(t, "querying for jobs that should run in second", normalizedTime)
	// TODO query database.
	// TODO check/document what happens with None values. add a graphiteQuery function that just gets last known value?
	// TODO: what do we do when timestamp is in future? or what if it's very old?
	jobs := []string{
		`median(graphite("apps.fakesite.web_server_01.counters.requests.count","2m","","")) < 0`,
		`median(graphite("avg(apps.fakesite.web_server_*.counters.requests.count)","2m","","")) > 1`,
	}
	for _, job := range jobs {
		queue <- Job{
			fmt.Sprintf("alert-id_%d", normalizedTime),
			job,
			t,
		}
	}
}

func Executor() {
	// TODO: once i have my own linux dev machine i can easily run docker and will nice authenticated requests to configured source
	gr := graphite.HostHeader(
		"play.grafana.org/api/datasources/proxy/1",
		http.Header{
			"X-Org-Id": []string{"7"},
		})

	for job := range queue {
		// TODO: ignore jobs already processed

		exp, err := expr.New(job.expr, expr.Graphite)
		if err != nil {
			// expressions should be validated before they are stored in the db
			// if they fail now it's a critical error
			panic(err)
		}

		// create cache
		// this is so that when bosun queries the same graphite query multiple times
		// like in (median(graphite("foo")> 10 || avg(graphite("foo") > 20)
		// it reuses the same resultsets internally.
		// cache is unbounded so that we are guaranteed internally consistent results
		// TODO recreate new cache at each second because cache is pointless at the next interval
		cacheObj := cache.New(0)

		// TODO once auth works, do it without rh. should work too because RH should only be used for bosun's own alerts and notifications
		rh := &sched.RunHistory{
			Cache:           cacheObj,
			Start:           job.ts, // this sets an explicit "until" to match the data this alert run is meant for, even when we are delayed
			Events:          make(map[expr.AlertKey]*sched.Event),
			Context:         nil,
			GraphiteContext: gr,
			Logstash:        make([]string, 0),
		}
		results, _, err := exp.Execute(rh.Context, rh.GraphiteContext, rh.Logstash, rh.Cache, nil, rh.Start, 0, true, nil, nil, nil)
		fmt.Println(job.ts, job.expr)
		for _, res := range results.Results {
			fmt.Println(res.Group, res.Value)
		}
		spew.Dump(err)
	}
}
