package main

import (
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"crypto/tls"
	"time"

	"encoding/json"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	namespace = "elasticsearch"
)

type VecInfo struct {
	help   string
	labels []string
}

var (
	gaugeMetrics = map[string]string{
		"cluster_health_status":                   "Current cluster health status",
		"cluster_health_timed_out":                "Current cluster health timed out",
		"cluster_number_of_nodes_total":           "Current cluster total node size",
		"cluster_number_of_data_nodes_total":      "Current cluster total data node size",
		"indices_fielddata_memory_size_bytes":     "Field data cache memory usage in bytes",
		"indices_filter_cache_memory_size_bytes":  "Filter cache memory usage in bytes",
		"indices_query_cache_memory_size_bytes":   "Query cache memory usage in bytes",
		"indices_request_cache_memory_size_bytes": "Request cache memory usage in bytes",
		"indices_docs":                            "Count of documents on this node",
		"indices_docs_deleted":                    "Count of deleted documents on this node",
		"indices_store_size_bytes":                "Current size of stored index data in bytes",
		"indices_segments_memory_bytes":           "Current memory size of segments in bytes",
		"indices_segments_count":                  "Count of index segments on this node",
		"process_cpu_percent":                     "Percent CPU used by process",
		"process_mem_resident_size_bytes":         "Resident memory in use by process in bytes",
		"process_mem_share_size_bytes":            "Shared memory in use by process in bytes",
		"process_mem_virtual_size_bytes":          "Total virtual memory used in bytes",
		"process_open_files_count":                "Open file descriptors",
		"process_max_files_count":                 "Max file descriptors for process",
	}
	counterMetrics = map[string]string{
		"indices_fielddata_evictions":           "Evictions from field data",
		"indices_filter_cache_evictions":        "Evictions from filter cache",
		"indices_query_cache_evictions":         "Evictions from query cache",
		"indices_request_cache_evictions":       "Evictions from request cache",
		"indices_flush_total":                   "Total flushes",
		"indices_flush_time_ms_total":           "Cumulative flush time in milliseconds",
		"transport_rx_packets_total":            "Count of packets received",
		"transport_rx_size_bytes_total":         "Total number of bytes received",
		"transport_tx_packets_total":            "Count of packets sent",
		"transport_tx_size_bytes_total":         "Total number of bytes sent",
		"indices_store_throttle_time_ms_total":  "Throttle time for index store in milliseconds",
		"indices_indexing_index_total":          "Total index calls",
		"indices_indexing_index_time_ms_total":  "Cumulative index time in milliseconds",
		"indices_merges_total":                  "Total merges",
		"indices_merges_total_docs_total":       "Cumulative docs merged",
		"indices_merges_total_size_bytes_total": "Total merge size in bytes",
		"indices_merges_total_time_ms_total":    "Total time spent merging in milliseconds",
		"indices_refresh_total":                 "Total refreshes",
		"indices_refresh_total_time_ms_total":   "Total time spent refreshing",
	}
	counterVecMetrics = map[string]*VecInfo{
		"jvm_gc_collection_seconds_count": &VecInfo{
			help:   "Count of JVM GC runs",
			labels: []string{"gc"},
		},
		"jvm_gc_collection_seconds_sum": &VecInfo{
			help:   "GC run time in seconds",
			labels: []string{"gc"},
		},
		"process_cpu_time_seconds_sum": &VecInfo{
			help:   "Process CPU time in seconds",
			labels: []string{"type"},
		},
		"thread_pool_completed_count": &VecInfo{
			help:   "Thread Pool operations completed",
			labels: []string{"type"},
		},
		"thread_pool_rejected_count": &VecInfo{
			help:   "Thread Pool operations rejected",
			labels: []string{"type"},
		},
	}

	gaugeVecMetrics = map[string]*VecInfo{
		"breakers_estimated_size_bytes": &VecInfo{
			help:   "Estimated size in bytes of breaker",
			labels: []string{"breaker"},
		},
		"breakers_limit_size_bytes": &VecInfo{
			help:   "Limit size in bytes for breaker",
			labels: []string{"breaker"},
		},
		"jvm_memory_committed_bytes": &VecInfo{
			help:   "JVM memory currently committed by area",
			labels: []string{"area"},
		},
		"jvm_memory_used_bytes": &VecInfo{
			help:   "JVM memory currently used by area",
			labels: []string{"area"},
		},
		"jvm_memory_max_bytes": &VecInfo{
			help:   "JVM memory max",
			labels: []string{"area"},
		},
		"thread_pool_active_count": &VecInfo{
			help:   "Thread Pool threads active",
			labels: []string{"type"},
		},
		"thread_pool_largest_count": &VecInfo{
			help:   "Thread Pool largest threads count",
			labels: []string{"type"},
		},
		"thread_pool_queue_count": &VecInfo{
			help:   "Thread Pool operations queued",
			labels: []string{"type"},
		},
		"thread_pool_threads_count": &VecInfo{
			help:   "Thread Pool current threads count",
			labels: []string{"type"},
		},
	}
)

// Exporter collects Elasticsearch stats from the given server and exports
// them using the prometheus metrics package.
type Exporter struct {
	URI        string
	ClusterURI string
	mutex      sync.RWMutex

	up prometheus.Gauge

	gauges      map[string]*prometheus.GaugeVec
	gaugeVecs   map[string]*prometheus.GaugeVec
	counters    map[string]*prometheus.CounterVec
	counterVecs map[string]*prometheus.CounterVec

	allNodes bool

	client *http.Client
}

// NewExporter returns an initialized Exporter.
func NewExporter(uri string, timeout time.Duration, allNodes bool, unsecure bool) *Exporter {
	counters := make(map[string]*prometheus.CounterVec, len(counterMetrics))
	counterVecs := make(map[string]*prometheus.CounterVec, len(counterVecMetrics))
	gauges := make(map[string]*prometheus.GaugeVec, len(gaugeMetrics))
	gaugeVecs := make(map[string]*prometheus.GaugeVec, len(gaugeVecMetrics))

	for name, info := range counterVecMetrics {
		counterVecs[name] = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      name,
			Help:      info.help,
		}, append([]string{"cluster", "node"}, info.labels...))
	}

	for name, info := range gaugeVecMetrics {
		gaugeVecs[name] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      name,
			Help:      info.help,
		}, append([]string{"cluster", "node"}, info.labels...))
	}

	for name, help := range counterMetrics {
		counters[name] = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      name,
			Help:      help,
		}, []string{"cluster", "node"})
	}

	for name, help := range gaugeMetrics {
		gauges[name] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      name,
			Help:      help,
		}, []string{"cluster", "node"})
	}

	var nodeURI string
	var clusterURI string
	if allNodes {
		nodeURI = uri + "/_nodes/stats"
	} else {
		nodeURI = uri + "/_nodes/_local/stats"
	}
	clusterURI = uri + "/_cluster/health"
	// Init our exporter.
	return &Exporter{
		URI:        nodeURI,
		ClusterURI: clusterURI,
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Was the Elasticsearch instance query successful?",
		}),

		counters:    counters,
		counterVecs: counterVecs,
		gauges:      gauges,
		gaugeVecs:   gaugeVecs,

		allNodes: allNodes,

		client: &http.Client{
			Transport: &http.Transport{
				Dial: func(netw, addr string) (net.Conn, error) {
					c, err := net.DialTimeout(netw, addr, timeout)
					if err != nil {
						return nil, err
					}
					if err := c.SetDeadline(time.Now().Add(timeout)); err != nil {
						return nil, err
					}
					return c, nil
				},
				TLSClientConfig: &tls.Config{InsecureSkipVerify: unsecure},
			},
		},
	}
}

// Describe describes all the metrics ever exported by the elasticsearch
// exporter. It implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.up.Desc()

	for _, vec := range e.counters {
		vec.Describe(ch)
	}

	for _, vec := range e.gauges {
		vec.Describe(ch)
	}

	for _, vec := range e.counterVecs {
		vec.Describe(ch)
	}

	for _, vec := range e.gaugeVecs {
		vec.Describe(ch)
	}

}

// Collect fetches the stats from configured elasticsearch location and
// delivers them as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()

	// Reset metrics.
	for _, vec := range e.gaugeVecs {
		vec.Reset()
	}

	for _, vec := range e.counterVecs {
		vec.Reset()
	}

	for _, vec := range e.gauges {
		vec.Reset()
	}

	for _, vec := range e.counters {
		vec.Reset()
	}

	defer func() { ch <- e.up }()

	resp, err := e.client.Get(e.URI)
	if err != nil {
		e.up.Set(0)
		log.Println("Error while querying Elasticsearch:", err)
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("Failed to read ES response body:", err)
		e.up.Set(0)
		return
	}

	e.up.Set(1)

	var allStats NodeStatsResponse
	err = json.Unmarshal(body, &allStats)
	if err != nil {
		log.Println("Failed to unmarshal JSON into struct:", err)
		return
	}

	// If we aren't polling all nodes, make sure we only got one response.
	if !e.allNodes && len(allStats.Nodes) != 1 {
		log.Println("Unexpected number of nodes returned.")
	}

	for _, stats := range allStats.Nodes {
		// GC Stats
		for collector, gcstats := range stats.JVM.GC.Collectors {
			e.counterVecs["jvm_gc_collection_seconds_count"].WithLabelValues(allStats.ClusterName, stats.Host, collector).Set(float64(gcstats.CollectionCount))
			e.counterVecs["jvm_gc_collection_seconds_sum"].WithLabelValues(allStats.ClusterName, stats.Host, collector).Set(float64(gcstats.CollectionTime / 1000))
		}

		// Breaker stats
		for breaker, bstats := range stats.Breakers {
			e.gaugeVecs["breakers_estimated_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host, breaker).Set(float64(bstats.EstimatedSize))
			e.gaugeVecs["breakers_limit_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host, breaker).Set(float64(bstats.LimitSize))
		}

		// Thread Pool stats
		for pool, pstats := range stats.ThreadPool {
			e.counterVecs["thread_pool_completed_count"].WithLabelValues(allStats.ClusterName, stats.Host, pool).Set(float64(pstats.Completed))
			e.counterVecs["thread_pool_rejected_count"].WithLabelValues(allStats.ClusterName, stats.Host, pool).Set(float64(pstats.Rejected))

			e.gaugeVecs["thread_pool_active_count"].WithLabelValues(allStats.ClusterName, stats.Host, pool).Set(float64(pstats.Active))
			e.gaugeVecs["thread_pool_threads_count"].WithLabelValues(allStats.ClusterName, stats.Host, pool).Set(float64(pstats.Active))
			e.gaugeVecs["thread_pool_largest_count"].WithLabelValues(allStats.ClusterName, stats.Host, pool).Set(float64(pstats.Active))
			e.gaugeVecs["thread_pool_queue_count"].WithLabelValues(allStats.ClusterName, stats.Host, pool).Set(float64(pstats.Active))
		}

		// JVM Memory Stats
		e.gaugeVecs["jvm_memory_committed_bytes"].WithLabelValues(allStats.ClusterName, stats.Host, "heap").Set(float64(stats.JVM.Mem.HeapCommitted))
		e.gaugeVecs["jvm_memory_used_bytes"].WithLabelValues(allStats.ClusterName, stats.Host, "heap").Set(float64(stats.JVM.Mem.HeapUsed))
		e.gaugeVecs["jvm_memory_max_bytes"].WithLabelValues(allStats.ClusterName, stats.Host, "heap").Set(float64(stats.JVM.Mem.HeapMax))
		e.gaugeVecs["jvm_memory_committed_bytes"].WithLabelValues(allStats.ClusterName, stats.Host, "non-heap").Set(float64(stats.JVM.Mem.NonHeapCommitted))
		e.gaugeVecs["jvm_memory_used_bytes"].WithLabelValues(allStats.ClusterName, stats.Host, "non-heap").Set(float64(stats.JVM.Mem.NonHeapUsed))

		// Indices Stats
		e.gauges["indices_fielddata_memory_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.FieldData.MemorySize))
		e.counters["indices_fielddata_evictions"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.FieldData.Evictions))

		e.gauges["indices_filter_cache_memory_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.FilterCache.MemorySize))
		e.counters["indices_filter_cache_evictions"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.FilterCache.Evictions))

		e.gauges["indices_query_cache_memory_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.QueryCache.MemorySize))
		e.counters["indices_query_cache_evictions"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.QueryCache.Evictions))

		e.gauges["indices_request_cache_memory_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.QueryCache.MemorySize))
		e.counters["indices_request_cache_evictions"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.QueryCache.Evictions))

		e.gauges["indices_docs"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Docs.Count))
		e.gauges["indices_docs_deleted"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Docs.Deleted))

		e.gauges["indices_segments_memory_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Segments.Memory))
		e.gauges["indices_segments_count"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Segments.Count))

		e.gauges["indices_store_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Store.Size))
		e.counters["indices_store_throttle_time_ms_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Store.ThrottleTime))

		e.counters["indices_flush_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Flush.Total))
		e.counters["indices_flush_time_ms_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Flush.Time))

		e.counters["indices_indexing_index_time_ms_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Indexing.IndexTime))
		e.counters["indices_indexing_index_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Indexing.IndexTotal))

		e.counters["indices_merges_total_time_ms_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Merges.TotalTime))
		e.counters["indices_merges_total_size_bytes_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Merges.TotalSize))
		e.counters["indices_merges_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Merges.Total))

		e.counters["indices_refresh_total_time_ms_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Refresh.TotalTime))
		e.counters["indices_refresh_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Indices.Refresh.Total))

		// Transport Stats
		e.counters["transport_rx_packets_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Transport.RxCount))
		e.counters["transport_rx_size_bytes_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Transport.RxSize))
		e.counters["transport_tx_packets_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Transport.TxCount))
		e.counters["transport_tx_size_bytes_total"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Transport.TxSize))

		// Process Stats
		e.gauges["process_cpu_percent"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Process.CPU.Percent))
		e.gauges["process_mem_resident_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Process.Memory.Resident))
		e.gauges["process_mem_share_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Process.Memory.Share))
		e.gauges["process_mem_virtual_size_bytes"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Process.Memory.TotalVirtual))
		e.gauges["process_open_files_count"].WithLabelValues(allStats.ClusterName, stats.Host).Set(float64(stats.Process.OpenFD))

		e.counterVecs["process_cpu_time_seconds_sum"].WithLabelValues(allStats.ClusterName, stats.Host, "total").Set(float64(stats.Process.CPU.Total / 1000))
		e.counterVecs["process_cpu_time_seconds_sum"].WithLabelValues(allStats.ClusterName, stats.Host, "sys").Set(float64(stats.Process.CPU.Sys / 1000))
		e.counterVecs["process_cpu_time_seconds_sum"].WithLabelValues(allStats.ClusterName, stats.Host, "user").Set(float64(stats.Process.CPU.User / 1000))
	}

	cresp, err := e.client.Get(e.ClusterURI)
	if err != nil {
		log.Println("Error while query elasticsearch cluster status", err)
		return
	}
	defer cresp.Body.Close()
	cBody, err := ioutil.ReadAll(cresp.Body)
	if err != nil {
		log.Println("Failed to read ES cluster response body:", err)
		return
	}
	var clusterStat ClusterHealthResponse
	err = json.Unmarshal(cBody, &clusterStat)
	if err != nil {
		log.Println("Failed to unmarshal JSON into struct:", err)
		return
	}
	switch clusterStat.Status {
	case "green":
		e.gauges["cluster_health_status"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(0))
	case "yellow":
		e.gauges["cluster_health_status"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(1))
	case "red":
		e.gauges["cluster_health_status"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(2))
	default:
		e.gauges["cluster_health_status"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(0))
	}
	if clusterStat.TimedOut {
		e.gauges["cluster_health_timed_out"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(1))
	} else {
		e.gauges["cluster_health_timed_out"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(0))
	}
	e.gauges["cluster_number_of_nodes_total"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(clusterStat.NumberOfNodes))
	e.gauges["cluster_number_of_data_nodes_total"].WithLabelValues(clusterStat.ClusterName, "unknown").Set(float64(clusterStat.NumberOfDataNodes))

	// Report metrics.

	for _, vec := range e.counterVecs {
		vec.Collect(ch)
	}

	for _, vec := range e.gaugeVecs {
		vec.Collect(ch)
	}

	for _, vec := range e.counters {
		vec.Collect(ch)
	}

	for _, vec := range e.gauges {
		vec.Collect(ch)
	}
}

func main() {
	var (
		listenAddress = flag.String("web.listen-address", ":9108", "Address to listen on for web interface and telemetry.")
		metricsPath   = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
		esURI         = flag.String("es.uri", "http://localhost:9200", "HTTP API address of an Elasticsearch node.")
		esTimeout     = flag.Duration("es.timeout", 5*time.Second, "Timeout for trying to get stats from Elasticsearch.")
		esAllNodes    = flag.Bool("es.all", false, "Export stats for all nodes in the cluster.")
		unsecure      = flag.Bool("es.unsecure", false, "Ignore certificate validation errors.")
	)
	flag.Parse()

	// if *esAllNodes {
	// 	*esURI = *esURI + "/_nodes/stats"
	// } else {
	// 	*esURI = *esURI + "/_nodes/_local/stats"
	// }

	exporter := NewExporter(*esURI, *esTimeout, *esAllNodes, *unsecure)
	prometheus.MustRegister(exporter)

	log.Println("Starting Server:", *listenAddress)
	http.Handle(*metricsPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>Elasticsearch Exporter</title></head>
             <body>
             <h1>Elasticsearch Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
