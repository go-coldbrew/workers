package workers_test

import (
	"context"
	"fmt"
	"time"

	"github.com/go-coldbrew/workers"
)

// solverHandler is used in Example_reconcilerWithChangeDetection to
// demonstrate the handler-as-metadata pattern.
type solverHandler struct {
	version int
}

func (h *solverHandler) RunCycle(ctx context.Context, _ *workers.WorkerInfo) error {
	<-ctx.Done()
	return ctx.Err()
}

func (h *solverHandler) Close() error { return nil }

// A simple worker that runs until cancelled.
func ExampleNewWorker() {
	w := workers.NewWorker("greeter").HandlerFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		fmt.Printf("worker %q started (attempt %d)\n", info.GetName(), info.GetAttempt())
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{w})
	// Output: worker "greeter" started (attempt 0)
}

// A worker with automatic restart on failure.
// The supervisor logs restart events; the worker succeeds on the third attempt.
func ExampleWorker_WithRestart() {
	attempt := 0
	w := workers.NewWorker("resilient").HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
		attempt++
		if attempt <= 2 {
			return fmt.Errorf("transient error")
		}
		fmt.Printf("succeeded on attempt %d\n", attempt)
		<-ctx.Done()
		return ctx.Err()
	}).WithRestart(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{w})
	// This example demonstrates restart behavior. Log output from the
	// supervisor is expected between restarts. The worker prints on success.
}

// A periodic worker that runs a function on a fixed interval.
func ExampleWorker_Every() {
	count := 0
	w := workers.NewWorker("ticker").HandlerFunc(func(_ context.Context, _ *workers.WorkerInfo) error {
		count++
		fmt.Printf("tick %d\n", count)
		return nil
	}).Every(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{w})
	// Output:
	// tick 1
	// tick 2
}

// Run multiple workers concurrently. All workers start together
// and stop when the context is cancelled.
func ExampleRun() {
	w1 := workers.NewWorker("api-poller").HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
		fmt.Println("api-poller started")
		<-ctx.Done()
		return ctx.Err()
	})
	w2 := workers.NewWorker("cache-warmer").HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
		fmt.Println("cache-warmer started")
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{w1, w2})
	fmt.Println("all workers stopped")
	// Unordered output:
	// api-poller started
	// cache-warmer started
	// all workers stopped
}

// RunWorker runs a single worker — useful for dynamic managers
// that spawn child workers in their own goroutines.
func ExampleRunWorker() {
	w := workers.NewWorker("single").HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
		fmt.Println("running")
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	workers.RunWorker(ctx, w)
	fmt.Println("done")
	// Output:
	// running
	// done
}

// EveryInterval wraps a function in a timer loop.
func ExampleEveryInterval() {
	count := 0
	fn := workers.EveryInterval(20*time.Millisecond, func(_ context.Context, _ *workers.WorkerInfo) error {
		count++
		fmt.Printf("tick %d\n", count)
		return nil
	})

	w := workers.NewWorker("periodic").HandlerFunc(fn)

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{w})
	// Output:
	// tick 1
	// tick 2
}

// ChannelWorker consumes items from a channel one at a time.
func ExampleChannelWorker() {
	ch := make(chan string, 3)
	ch <- "hello"
	ch <- "world"
	ch <- "!"
	close(ch)

	fn := workers.ChannelWorker(ch, func(_ context.Context, _ *workers.WorkerInfo, item string) error {
		fmt.Println(item)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	w := workers.NewWorker("consumer").HandlerFunc(fn)
	workers.Run(ctx, []*workers.Worker{w})
	// Output:
	// hello
	// world
	// !
}

// BatchChannelWorker collects items into batches and flushes on
// maxSize or maxDelay — whichever comes first.
func ExampleBatchChannelWorker() {
	ch := make(chan int, 10)
	for i := 1; i <= 6; i++ {
		ch <- i
	}
	close(ch)

	fn := workers.BatchChannelWorker(ch, 3, time.Hour, func(_ context.Context, _ *workers.WorkerInfo, batch []int) error {
		fmt.Println(batch)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	w := workers.NewWorker("batcher").HandlerFunc(fn)
	workers.Run(ctx, []*workers.Worker{w})
	// Output:
	// [1 2 3]
	// [4 5 6]
}

// Standalone usage with signal handling — no ColdBrew required.
func Example_standalone() {
	// In production you'd use signal.NotifyContext(ctx, os.Interrupt).
	// For the example, use a short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{
		workers.NewWorker("kafka").HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
			fmt.Println("consuming messages")
			<-ctx.Done()
			return ctx.Err()
		}),
	})
	fmt.Println("shutdown complete")
	// Output:
	// consuming messages
	// shutdown complete
}

// A manager worker that dynamically spawns and removes child workers
// using WorkerInfo.Add, Remove, and GetChildren.
func ExampleWorkerInfo_Add() {
	manager := workers.NewWorker("manager").HandlerFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		// Spawn two child workers dynamically.
		info.Add(workers.NewWorker("child-a").HandlerFunc(func(ctx context.Context, childInfo *workers.WorkerInfo) error {
			fmt.Printf("%s started\n", childInfo.GetName())
			<-ctx.Done()
			return ctx.Err()
		}))
		info.Add(workers.NewWorker("child-b").HandlerFunc(func(ctx context.Context, childInfo *workers.WorkerInfo) error {
			fmt.Printf("%s started\n", childInfo.GetName())
			<-ctx.Done()
			return ctx.Err()
		}))

		// Give children time to start.
		time.Sleep(30 * time.Millisecond)
		fmt.Printf("children: %v\n", info.GetChildren())

		// Remove one child.
		info.Remove("child-a")
		time.Sleep(30 * time.Millisecond)
		fmt.Printf("after remove: %v\n", info.GetChildren())

		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{manager})
	// Unordered output:
	// child-a started
	// child-b started
	// children: [child-a child-b]
	// after remove: [child-b]
}

// Replace a child worker using Remove + Add.
// Add is a no-op if the name exists, so Remove first.
func ExampleWorkerInfo_Add_replace() {
	manager := workers.NewWorker("manager").HandlerFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		info.Add(workers.NewWorker("processor").HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
			fmt.Println("processor v1")
			<-ctx.Done()
			return ctx.Err()
		}))
		time.Sleep(30 * time.Millisecond)

		// Replace: Remove the old worker, then Add the new one.
		info.Remove("processor")
		time.Sleep(10 * time.Millisecond) // brief gap while old stops
		info.Add(workers.NewWorker("processor").HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
			fmt.Println("processor v2")
			<-ctx.Done()
			return ctx.Err()
		}))
		time.Sleep(30 * time.Millisecond)

		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{manager})
	// Output:
	// processor v1
	// processor v2
}

// Simulates a config-driven worker pool manager that reconciles
// desired workers against running workers on each tick.
func Example_dynamicWorkerPool() {
	// Simulate config that changes over 3 ticks.
	// Tick 1: start worker-a
	// Tick 2: add worker-b
	// Tick 3: remove worker-a
	configs := [][]string{
		{"worker-a"},
		{"worker-a", "worker-b"},
		{"worker-b"},
	}

	tick := 0
	manager := workers.NewWorker("pool-manager").HandlerFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if tick >= len(configs) {
					continue
				}
				desired := map[string]bool{}
				for _, name := range configs[tick] {
					desired[name] = true
				}
				tick++

				running := map[string]bool{}
				for _, name := range info.GetChildren() {
					running[name] = true
				}

				// Remove workers no longer desired.
				for name := range running {
					if !desired[name] {
						info.Remove(name)
					}
				}
				// Add workers that aren't already running.
				for name := range desired {
					if !running[name] {
						info.Add(workers.NewWorker(name).HandlerFunc(func(ctx context.Context, _ *workers.WorkerInfo) error {
							<-ctx.Done()
							return ctx.Err()
						}))
					}
				}
				time.Sleep(10 * time.Millisecond) // let children start
				fmt.Printf("tick %d: children=%v\n", tick, info.GetChildren())
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{manager})
	fmt.Println("pool shut down")
	// Output:
	// tick 1: children=[worker-a]
	// tick 2: children=[worker-a worker-b]
	// tick 3: children=[worker-b]
	// pool shut down
}

// Demonstrates config-driven reconciliation with change detection using
// the handler-as-metadata pattern. The handler struct carries a config
// version that the reconciler inspects via GetChild().GetHandler() type
// assertion, eliminating the need for a parallel tracking map.
func Example_reconcilerWithChangeDetection() {
	type solverConfig struct {
		version int
		name    string
	}

	// Simulate config that changes over 3 ticks.
	configs := []map[string]solverConfig{
		{"a": {version: 1, name: "a"}},
		{"a": {version: 1, name: "a"}, "b": {version: 1, name: "b"}},
		{"a": {version: 2, name: "a"}, "b": {version: 1, name: "b"}}, // a gets new version
	}

	tick := 0
	manager := workers.NewWorker("reconciler").HandlerFunc(func(ctx context.Context, info *workers.WorkerInfo) error {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if tick >= len(configs) {
					continue
				}
				desired := configs[tick]
				tick++

				// Remove workers no longer desired.
				for _, name := range info.GetChildren() {
					if _, ok := desired[name]; !ok {
						info.Remove(name)
					}
				}

				// Add new or replace changed workers.
				for key, cfg := range desired {
					child, exists := info.GetChild(key)
					if exists {
						// Check if config changed via handler type assertion.
						if h, ok := child.GetHandler().(*solverHandler); ok && h.version == cfg.version {
							continue // unchanged, skip
						}
						info.Remove(key) // config changed, replace
					}
					info.Add(workers.NewWorker(key).Handler(&solverHandler{version: cfg.version}))
				}
				time.Sleep(10 * time.Millisecond)
				fmt.Printf("tick %d: children=%v count=%d\n", tick, info.GetChildren(), len(info.GetChildren()))
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{manager})
	// Output:
	// tick 1: children=[a] count=1
	// tick 2: children=[a b] count=2
	// tick 3: children=[a b] count=2
}

// Per-worker middleware using the interceptor pattern.
func ExampleWorker_Interceptors() {
	loggingMW := func(ctx context.Context, info *workers.WorkerInfo, next workers.CycleFunc) error {
		fmt.Printf("[%s] cycle start\n", info.GetName())
		err := next(ctx, info)
		fmt.Printf("[%s] cycle end\n", info.GetName())
		return err
	}

	w := workers.NewWorker("with-logging").
		HandlerFunc(func(_ context.Context, info *workers.WorkerInfo) error {
			fmt.Printf("[%s] doing work\n", info.GetName())
			return nil
		}).
		Every(20 * time.Millisecond).
		Interceptors(loggingMW)

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	workers.Run(ctx, []*workers.Worker{w})
	// Output:
	// [with-logging] cycle start
	// [with-logging] doing work
	// [with-logging] cycle end
}
