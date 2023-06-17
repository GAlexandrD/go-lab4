package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/roman-mazur/design-practice-2-template/httptools"
	"github.com/roman-mazur/design-practice-2-template/signal"
)

var (
	port       = flag.Int("port", 8090, "load balancer port")
	timeoutSec = flag.Int("timeout-sec", 3, "request timeout time in seconds")
	https      = flag.Bool("http", false, "whether backends support HTTPs")

	traceEnabled = flag.Bool("trace", false, "whether to include tracing information into responses")
)

type serverType struct {
	dst             string
	dataTransferred int64
	isWorking       *atomic.Bool
}

var (
	timeout     = time.Duration(*timeoutSec) * time.Second
	serversPool = []serverType{
		{
			dst:             "server1:8080",
			dataTransferred: 0,
			isWorking: &atomic.Bool{},
		}, {
			dst:             "server2:8080",
			dataTransferred: 0,
			isWorking: &atomic.Bool{},
		}, {
			dst:             "server3:8080",
			dataTransferred: 0,
			isWorking: &atomic.Bool{},
		},
	}
)

type healthCheckerInterface interface {
	health(dst string) bool
}

type Balancer struct {
	pool []serverType
	hc   healthCheckerInterface
	curMin int64
}

func scheme() string {
	if *https {
		return "https"
	}
	return "http"
}

func SetupBalancer() *Balancer{
	balancer := &Balancer{
		pool: serversPool,
		hc:   &healthChecker{},
	}
	balancer.runChecker()
	balancer.Start()
	return balancer
}

func (b *Balancer) runChecker() {
	for i := range b.pool {
		server := &b.pool[i]
		go func() {
			isWorking := b.hc.health(server.dst)
			server.isWorking.Swap(isWorking)
			for range time.Tick(10 * time.Second) {
				isWorking := b.hc.health(server.dst)
				server.isWorking.Swap(isWorking)
				log.Printf("[ %s data: %d isWorking: %t ]", server.dst, server.dataTransferred, isWorking)
			}
		}()
	}
}

func (b *Balancer) Start() {
	frontend := httptools.CreateServer(*port, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		index, err := b.getIndex()
		if err != nil {
			log.Println(err.Error())
		} else {
			b.forward(&b.pool[index], rw, r)
		}
	}))

	log.Println("Starting load balancer...")
	log.Printf("Tracing support enabled: %t", *traceEnabled)
	frontend.Start()
	signal.WaitForTerminationSignal()
}

type healthChecker struct{}

func (hc *healthChecker) health(dst string) bool {
	ctx, _ := context.WithTimeout(context.Background(), timeout)
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s://%s/health", scheme(), dst), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return true
}

var cnt int = 0

func (b *Balancer) forward(server *serverType, rw http.ResponseWriter, r *http.Request) error {
	ctx, _ := context.WithTimeout(r.Context(), timeout)
	fwdRequest := r.Clone(ctx)
	fwdRequest.RequestURI = ""
	fwdRequest.URL.Host = server.dst
	fwdRequest.URL.Scheme = scheme()
	fwdRequest.Host = server.dst
	resp, err := http.DefaultClient.Do(fwdRequest)
	if err == nil {

		// count length of server response and save it
		length := headerLength(resp.Header) + resp.ContentLength
		atomic.AddInt64(&server.dataTransferred, length)
		if server.dataTransferred > b.curMin {
			atomic.SwapInt64(&b.curMin, server.dataTransferred)
		}


		for k, values := range resp.Header {
			for _, value := range values {
				rw.Header().Add(k, value)
			}
		}
		if *traceEnabled {
			rw.Header().Set("lb-from", server.dst)
			rw.Header().Set("lb-size", strconv.Itoa(int(length)))
		}
		log.Println("fwd", resp.StatusCode, resp.Request.URL)
		rw.WriteHeader(resp.StatusCode)
		defer resp.Body.Close()
		_, err := io.Copy(rw, resp.Body)
		if err != nil {
			log.Printf("Failed to write response: %s", err)
		}
		return nil
	} else {
		log.Printf("Failed to get response from %s: %s", server.dst, err)
		rw.WriteHeader(http.StatusServiceUnavailable)
		return err
	}
}

func main() {
	flag.Parse()
	SetupBalancer()
}

func (b *Balancer) getIndex() (int, error) {
	workingPool := []int{}
	equal := []int{}
	for i := range b.pool {
		if !b.pool[i].isWorking.Load() {
			continue
		}
		workingPool = append(workingPool, i)
		curData := b.pool[i].dataTransferred
		if curData < b.curMin {
			return i, nil
		}
		if curData == b.curMin {
			equal = append(equal, i)
		}
	}
	if len(equal) != 0 {
		return equal[0], nil
	}
	if len(workingPool) == 0 {
		return 0, errors.New("There are no servers available")
	}
	return workingPool[0], nil
}

func headerLength(header http.Header) int64 {
	var str string
	for key, values := range header {
		for _, value := range values {
			str += fmt.Sprintf("%s: %s\n", key, value)
		}
	}
	byteSlice := []byte(str)
	byteCount := len(byteSlice)
	return int64(byteCount)
}
