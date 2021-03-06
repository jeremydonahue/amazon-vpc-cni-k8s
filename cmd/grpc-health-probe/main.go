package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

var (
	userAgent      string
	remoteURL      string
	serviceName    string
	connTimeoutDur time.Duration = time.Second
	rpcTimeoutDur  time.Duration = time.Second
	verbose        bool
)

const (
	// StatusInvalidArguments indicates specified invalid arguments.
	StatusInvalidArguments = 1
	// StatusConnectionFailure indicates connection failed.
	StatusConnectionFailure = 2
	// StatusRPCFailure indicates rpc failed.
	StatusRPCFailure = 3
	// StatusUnhealthy indicates rpc succeeded but indicates unhealthy service.
	StatusUnhealthy = 4
)

func init() {
	log.SetFlags(0)
	flag.StringVar(&remoteURL, "addr", "", "(required) tcp host:port to connect")
	flag.StringVar(&serviceName, "service", "", "service name to check (default: \"\")")
	flag.StringVar(&userAgent, "user-agent", "grpc-health-probe", "user-agent header value of health check requests")
	// timeouts
	flag.DurationVar(&connTimeoutDur, "connect-timeout", connTimeoutDur, "timeout for establishing connection")
	flag.DurationVar(&rpcTimeoutDur, "rpc-timeout", rpcTimeoutDur, "timeout for health check rpc")
	// verbose
	flag.BoolVar(&verbose, "v", false, "verbose logs")

	flag.Parse()

	argError := func(s string, v ...interface{}) {
		log.Printf("error: "+s, v...)
		os.Exit(StatusInvalidArguments)
	}

	if remoteURL == "" {
		argError("--addr not specified")
	}

	if connTimeoutDur <= 0 {
		argError("--connect-timeout must be greater than zero (specified: %v)", connTimeoutDur)
	}
	if rpcTimeoutDur <= 0 {
		argError("--rpc-timeout must be greater than zero (specified: %v)", rpcTimeoutDur)
	}
	if verbose {
		log.Printf("parsed options:")
		log.Printf("> remoteUrl=%s conn-timeout=%v rpc-timeout=%v", remoteURL, connTimeoutDur, rpcTimeoutDur)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		sig := <-c
		if sig == os.Interrupt {
			log.Printf("cancellation received")
			cancel()
			return
		}
	}()

	opts := []grpc.DialOption{
		grpc.WithUserAgent(userAgent),
		grpc.WithBlock()}

	opts = append(opts, grpc.WithInsecure())

	if verbose {
		log.Print("establishing connection")
	}
	connStart := time.Now()
	dialCtx, cancel2 := context.WithTimeout(ctx, connTimeoutDur)
	defer cancel2()
	conn, err := grpc.DialContext(dialCtx, remoteURL, opts...)
	if err != nil {
		if err == context.DeadlineExceeded {
			log.Printf("timeout: failed to connect service %q within %v", remoteURL, connTimeoutDur)
		} else {
			log.Printf("error: failed to connect service at %q: %+v", remoteURL, err)
		}
		os.Exit(StatusConnectionFailure)
	}
	connDuration := time.Since(connStart)
	defer conn.Close()
	if verbose {
		log.Printf("connection establisted (took %v)", connDuration)
	}

	rpcStart := time.Now()
	rpcCtx, rpcCancel := context.WithTimeout(ctx, rpcTimeoutDur)
	defer rpcCancel()
	resp, err := healthpb.NewHealthClient(conn).Check(rpcCtx, &healthpb.HealthCheckRequest{Service: serviceName})
	log.Print(resp)
	if err != nil {
		if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
			log.Printf("error: this server does not implement the grpc health protocol (grpc.health.v1.Health)")
		} else if stat, ok := status.FromError(err); ok && stat.Code() == codes.DeadlineExceeded {
			log.Printf("timeout: health rpc did not complete within %v", rpcTimeoutDur)
		} else {
			log.Printf("error: health rpc failed: %+v", err)
		}
		os.Exit(StatusRPCFailure)
	}
	rpcDuration := time.Since(rpcStart)

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		log.Printf("service unhealthy (responded with %q)", resp.GetStatus().String())
		os.Exit(StatusUnhealthy)
	}
	if verbose {
		log.Printf("time elapsed: connect=%v rpc=%v", connDuration, rpcDuration)
	}
	log.Printf("status: %v", resp.GetStatus().String())
}
