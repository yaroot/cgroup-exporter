package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/containerd/cgroups"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	versionFlag = flag.Bool("version", false, "version")
	version     string
	git         string

	address = flag.String("address", ":48900", "address")
)

func main() {
	flag.Parse()
	if *versionFlag {
		fmt.Printf("version %s, git %s\n", version, git)
		return
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	system, err := cgroups.Load(subsystem, cgroups.StaticPath("/system.slice"))
	if err != nil {
		log.Fatalf("cgroups load: %s", err)
	}

	http.HandleFunc("/metrics", exportMetrics(system))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(200)
			return
		}
		w.Header().Add("Location", "/metrics")
		w.WriteHeader(302)
	})

	server := &http.Server{
		Addr: *address,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http server ListenAndServe: %v", err)
		}
	}()

	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("http server shutdown: %s", err)
	}
}

func subsystem() ([]cgroups.Subsystem, error) {
	root := "/sys/fs/cgroup"
	s := []cgroups.Subsystem{
		cgroups.NewDevices(root),
		cgroups.NewCpuacct(root),
		cgroups.NewMemory(root),
	}
	return s, nil
}

func exportMetrics(system cgroups.Cgroup) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		processes, err := system.Processes(cgroups.Devices, true)
		if err != nil {
			msg := fmt.Sprintf("cgroups load: %s", err)
			http.Error(w, msg, http.StatusInternalServerError)
		}

		groups := make(map[string]*cgroups.Metrics, len(processes))
		for _, p := range processes {
			name := strings.TrimPrefix(p.Path, "/sys/fs/cgroup/devices")
			name = strings.TrimSuffix(name, "/")
			name = strings.ReplaceAll(name, "\\x2d", "-")
			if _, ok := groups[name]; ok {
				continue
			}

			control, err := cgroups.Load(subsystem, func(subsystem cgroups.Name) (string, error) {
				return name, nil
			})
			if err != nil {
				log.Printf("cgroups load: %s", err)
				continue
			}
			stats, err := control.Stat(cgroups.IgnoreNotExist)
			if err != nil {
				log.Printf("control stat: %s", err)
				continue
			}
			groups[name] = stats
		}

		fmt.Fprintln(w, `# HELP container_cpu_user_seconds_total Cumulative user cpu time consumed in seconds.`)
		fmt.Fprintln(w, `# TYPE container_cpu_user_seconds_total counter`)
		for name, stats := range groups {
			fmt.Fprintf(w, `container_cpu_user_seconds_total{id=%s} %.2f`, strconv.Quote(name), float64(stats.CPU.Usage.User)/1000000000.0)
			fmt.Fprintln(w)
		}

		fmt.Fprintln(w, `# HELP container_memory_usage_bytes Current memory usage in bytes, including all memory regardless of when it was accessed`)
		fmt.Fprintln(w, `# TYPE container_memory_usage_bytes gauge`)
		for name, stats := range groups {
			fmt.Fprintf(w, `container_memory_usage_bytes{id=%s} %d`, strconv.Quote(name), stats.Memory.Usage.Usage)
			fmt.Fprintln(w)
		}

		fmt.Fprintln(w, `# HELP container_memory_rss Size of RSS in bytes.`)
		fmt.Fprintln(w, `# TYPE container_memory_rss gauge`)
		for name, stats := range groups {
			fmt.Fprintf(w, `container_memory_rss{id=%s} %d`, strconv.Quote(name), stats.Memory.RSS)
			fmt.Fprintln(w)
		}

		fmt.Fprintln(w, `# HELP container_memory_cache Size of Cache in bytes.`)
		fmt.Fprintln(w, `# TYPE container_memory_cache gauge`)
		for name, stats := range groups {
			fmt.Fprintf(w, `container_memory_cache{id=%s} %d`, strconv.Quote(name), stats.Memory.Cache)
			fmt.Fprintln(w)
		}

		fmt.Fprintln(w, `# HELP container_memory_file_mapped Size of Mapped file in bytes.`)
		fmt.Fprintln(w, `# TYPE container_memory_file_mapped gauge`)
		for name, stats := range groups {
			fmt.Fprintf(w, `container_memory_file_mapped{id=%s} %d`, strconv.Quote(name), stats.Memory.MappedFile)
			fmt.Fprintln(w)
		}

		fmt.Fprintln(w, `# HELP container_memory_file_dirty Size of Dirty file in bytes.`)
		fmt.Fprintln(w, `# TYPE container_memory_file_dirty gauge`)
		for name, stats := range groups {
			fmt.Fprintf(w, `container_memory_file_dirty{id=%s} %d`, strconv.Quote(name), stats.Memory.Dirty)
			fmt.Fprintln(w)
		}

		return
	}
}
