package main

import (
	"fmt"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jamiealquiza/polymur/api"
	"github.com/jamiealquiza/polymur/listener"
	"github.com/jamiealquiza/polymur/output"
	"github.com/jamiealquiza/polymur/pool"
	"github.com/jamiealquiza/polymur/statstracker"
	"github.com/jamiealquiza/runstats"

	"github.com/jamiealquiza/envy"
)

var (
	options struct {
		addr             string
		apiAddr          string
		statAddr         string
		incomingQueuecap int
		outgoingQueuecap int
		console          bool
		destinations     string
		metricsFlush     int
		distribution     string
	}

	sigChan = make(chan os.Signal)
)

func init() {
	flag.StringVar(&options.addr, "listen-addr", "0.0.0.0:2003", "Polymur listen address")
	flag.StringVar(&options.apiAddr, "api-addr", "localhost:2030", "API listen address")
	flag.StringVar(&options.statAddr, "stat-addr", "localhost:2020", "runstats listen address")
	flag.IntVar(&options.outgoingQueuecap, "outgoing-queue-cap", 4096, "In-flight message queue capacity per destination (number of data points)")
	flag.IntVar(&options.incomingQueuecap, "incoming-queue-cap", 32768, "In-flight incoming message queue capacity (number of data point batches [100 points max per batch])")
	flag.BoolVar(&options.console, "console-out", false, "Dump output to console")
	flag.StringVar(&options.destinations, "destinations", "", "Comma-delimited list of ip:port destinations")
	flag.IntVar(&options.metricsFlush, "metrics-flush", 0, "Graphite flush interval for runtime metrics (0 is disabled)")
	flag.StringVar(&options.distribution, "distribution", "broadcast", "Destination distribution methods: broadcast, hash-route")

	envConfig := os.Getenv("POLYMUR_OUTGOING_QUEUE_CAP")
	if envConfig != "" {
		fmt.Printf("XXX queue cap env var set: %s\n", envConfig)
	} else {
		fmt.Println("XXX queue cap env var unset")
	}

	envy.Parse("POLYMUR")
	flag.Parse()
}

// Handles signal events.
func runControl() {
	signal.Notify(sigChan, syscall.SIGINT)
	<-sigChan
	log.Printf("Shutting down")
	os.Exit(0)
}

func main() {
	log.Println("::: Polymur :::")
	ready := make(chan bool, 1)

	incomingQueue := make(chan []*string, options.incomingQueuecap)

	pool := pool.NewPool()

	fmt.Printf("XXX queue cap config: %d\n", options.outgoingQueuecap)

	// Output writer.
	if options.console {
		go output.Console(incomingQueue)
		ready <- true
	} else {
		go output.TCPWriter(
			pool,
			&output.TCPWriterConfig{
				Destinations:  options.destinations,
				Distribution:  options.distribution,
				IncomingQueue: incomingQueue,
				QueueCap:      options.outgoingQueuecap,
			},
			ready)
	}

	<-ready

	// Stat counters.
	sentCntr := &statstracker.Stats{}
	go statstracker.StatsTracker(pool, sentCntr)

	// TCP Listener.
	go listener.TCPListener(&listener.TCPListenerConfig{
		Addr:          options.addr,
		IncomingQueue: incomingQueue,
		FlushTimeout:  5,
		FlushSize:     100,
		Stats:         sentCntr,
	})

	// API listener.
	go api.API(pool, options.apiAddr)

	// Polymur stats writer.
	if options.metricsFlush > 0 {
		go runstats.WriteGraphiteWithBackendMetrics(pool, incomingQueue, options.incomingQueuecap, options.metricsFlush, sentCntr)
	}

	// Runtime stats listener.
	go runstats.Start(options.statAddr)

	runControl()
}
