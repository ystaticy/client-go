// Copyright 2021 TiKV Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// NOTE: The code in this file is based on code from the
// TiDB project, licensed under the Apache License v 2.0
//
// https://github.com/pingcap/tidb/tree/cc5e161ac06827589c4966674597c137cc9e809c/store/tikv/range_task.go
//

// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rangetask

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/tikv/client-go/v2/config/retry"
	"github.com/tikv/client-go/v2/internal/logutil"
	"github.com/tikv/client-go/v2/kv"
	"github.com/tikv/client-go/v2/metrics"
	"github.com/tikv/client-go/v2/util/redact"
	"go.uber.org/zap"
)

const (
	rangeTaskDefaultStatLogInterval = time.Minute * 10
	defaultRegionsPerTask           = 128

	lblCompletedRegions = "completed-regions"
	lblFailedRegions    = "failed-regions"
)

// Runner splits a range into many ranges to process concurrently, and convenient to send requests to all
// regions in the range. Because of merging and splitting, it's possible that multiple requests for disjoint ranges are
// sent to the same region.
type Runner struct {
	// name is consistent across all runners of the same type, which is used for metrics
	name string
	// identifier can be a unique identifier for each runner, which is used for logging
	identifier      string
	store           storage
	concurrency     int
	handler         TaskHandler
	statLogInterval time.Duration
	regionsPerTask  int

	completedRegions int32
	failedRegions    int32
}

// TaskStat is used to count Regions that completed or failed to do the task.
type TaskStat struct {
	CompletedRegions int
	FailedRegions    int
}

// TaskHandler is the type of functions that processes a task of a key range.
// The function should calculate Regions that succeeded or failed to the task.
// Returning error from the handler means the error caused the whole task should be stopped.
type TaskHandler = func(ctx context.Context, r kv.KeyRange) (TaskStat, error)

// NewRangeTaskRunner creates a RangeTaskRunner.
//
// `requestCreator` is the function used to create RPC request according to the given range.
// `responseHandler` is the function to process responses of errors. If `responseHandler` returns error, the whole job
// will be canceled.
func NewRangeTaskRunner(
	name string,
	store storage,
	concurrency int,
	handler TaskHandler,
) *Runner {
	return NewRangeTaskRunnerWithID(
		name,
		"",
		store,
		concurrency,
		handler,
	)
}

// NewRangeTaskRunnerWithID creates a RangeTaskRunner with a specified identifier
func NewRangeTaskRunnerWithID(
	name string,
	identifier string,
	store storage,
	concurrency int,
	handler TaskHandler,
) *Runner {
	id := identifier
	if len(id) == 0 {
		id = name
	}
	return &Runner{
		name:            name,
		identifier:      id,
		store:           store,
		concurrency:     concurrency,
		handler:         handler,
		statLogInterval: rangeTaskDefaultStatLogInterval,
		regionsPerTask:  defaultRegionsPerTask,
	}
}

// SetStatLogInterval sets the statsLogInterval
func (s *Runner) SetStatLogInterval(interval time.Duration) {
	s.statLogInterval = interval
}

// SetRegionsPerTask sets how many regions is in a divided task. Since regions may split and merge, it's possible that
// a sub task contains not exactly specified number of regions.
func (s *Runner) SetRegionsPerTask(regionsPerTask int) {
	if regionsPerTask < 1 {
		panic("RangeTaskRunner: regionsPerTask should be at least 1")
	}
	s.regionsPerTask = regionsPerTask
}

const locateRegionMaxBackoff = 20000

// NewLocateRegionBackoffer creates the backoofer for LocateRegion request.
func NewLocateRegionBackoffer(ctx context.Context) *retry.Backoffer {
	return retry.NewBackofferWithVars(ctx, locateRegionMaxBackoff, nil)
}

// RunOnRange runs the task on the given range.
// Empty startKey or endKey means unbounded.
func (s *Runner) RunOnRange(ctx context.Context, startKey, endKey []byte) error {
	s.completedRegions = 0
	metrics.TiKVRangeTaskStats.WithLabelValues(s.name, lblCompletedRegions).Set(0)

	if len(endKey) != 0 && bytes.Compare(startKey, endKey) >= 0 {
		logutil.Logger(ctx).Info("empty range task executed. ignored",
			zap.String("name", s.identifier),
			zap.String("startKey", redact.Key(startKey)),
			zap.String("endKey", redact.Key(endKey)))
		return nil
	}

	logutil.Logger(ctx).Info("range task started",
		zap.String("name", s.identifier),
		zap.String("startKey", redact.Key(startKey)),
		zap.String("endKey", redact.Key(endKey)),
		zap.Int("concurrency", s.concurrency))

	// Periodically log the progress
	statLogTicker := time.NewTicker(s.statLogInterval)

	ctx, cancel := context.WithCancel(ctx)
	taskCh := make(chan *kv.KeyRange, s.concurrency)
	var wg sync.WaitGroup

	// Create workers that concurrently process the whole range.
	workers := make([]*rangeTaskWorker, 0, s.concurrency)
	for i := 0; i < s.concurrency; i++ {
		w := s.createWorker(taskCh, &wg)
		workers = append(workers, w)
		wg.Add(1)
		go w.run(ctx, cancel)
	}

	startTime := time.Now()

	// Make sure taskCh is closed exactly once
	isClosed := false
	defer func() {
		if !isClosed {
			close(taskCh)
			wg.Wait()
		}
		statLogTicker.Stop()
		cancel()
		metrics.TiKVRangeTaskStats.WithLabelValues(s.name, lblCompletedRegions).Set(0)
	}()

	// Iterate all regions and send each region's range as a task to the workers.
	key := startKey
Loop:
	for {
		select {
		case <-statLogTicker.C:
			logutil.Logger(ctx).Info("range task in progress",
				zap.String("name", s.identifier),
				zap.String("startKey", redact.Key(startKey)),
				zap.String("endKey", redact.Key(endKey)),
				zap.Int("concurrency", s.concurrency),
				zap.Duration("cost time", time.Since(startTime)),
				zap.Int("completed regions", s.CompletedRegions()))
		default:
		}

		bo := NewLocateRegionBackoffer(ctx)

		rangeEndKey, err := s.store.GetRegionCache().BatchLoadRegionsFromKey(bo, key, s.regionsPerTask)
		if err != nil {
			logutil.Logger(ctx).Info("range task try to get range end key failure",
				zap.String("name", s.identifier),
				zap.String("startKey", redact.Key(startKey)),
				zap.String("endKey", redact.Key(endKey)),
				zap.String("loadRegionKey", redact.Key(key)),
				zap.Duration("cost time", time.Since(startTime)),
				zap.Error(err))
			return err
		}
		task := &kv.KeyRange{
			StartKey: key,
			EndKey:   rangeEndKey,
		}

		isLast := len(task.EndKey) == 0 || (len(endKey) > 0 && bytes.Compare(task.EndKey, endKey) >= 0)
		// Let task.EndKey = min(endKey, loc.EndKey)
		if isLast {
			task.EndKey = endKey
		}

		pushTaskStartTime := time.Now()

		select {
		case taskCh <- task:
		case <-ctx.Done():
			break Loop
		}
		metrics.TiKVRangeTaskPushDuration.WithLabelValues(s.name).Observe(time.Since(pushTaskStartTime).Seconds())

		if isLast {
			break
		}

		key = task.EndKey
	}

	isClosed = true
	close(taskCh)
	wg.Wait()
	for _, w := range workers {
		if w.err != nil {
			logutil.Logger(ctx).Info("range task failed",
				zap.String("name", s.identifier),
				zap.String("startKey", redact.Key(startKey)),
				zap.String("endKey", redact.Key(endKey)),
				zap.Duration("cost time", time.Since(startTime)),
				zap.Int("completed regions", s.CompletedRegions()),
				zap.Int("failed regions", s.FailedRegions()),
				zap.Error(w.err))
			return errors.WithStack(w.err)
		}
	}

	logutil.Logger(ctx).Info("range task finished",
		zap.String("name", s.identifier),
		zap.String("startKey", redact.Key(startKey)),
		zap.String("endKey", redact.Key(endKey)),
		zap.Duration("cost time", time.Since(startTime)),
		zap.Int("completed regions", s.CompletedRegions()))

	return nil
}

// createWorker creates a worker that can process tasks from the given channel.
func (s *Runner) createWorker(taskCh chan *kv.KeyRange, wg *sync.WaitGroup) *rangeTaskWorker {
	return &rangeTaskWorker{
		name:       s.name,
		identifier: s.identifier,
		store:      s.store,
		handler:    s.handler,
		taskCh:     taskCh,
		wg:         wg,

		completedRegions: &s.completedRegions,
		failedRegions:    &s.failedRegions,
	}
}

// CompletedRegions returns how many regions has been sent requests.
func (s *Runner) CompletedRegions() int {
	return int(atomic.LoadInt32(&s.completedRegions))
}

// FailedRegions returns how many regions has failed to do the task.
func (s *Runner) FailedRegions() int {
	return int(atomic.LoadInt32(&s.failedRegions))
}

// rangeTaskWorker is used by RangeTaskRunner to process tasks concurrently.
type rangeTaskWorker struct {
	// name is consistent across all runners of the same type, which is used for metrics
	name string
	// identifier can be a unique identifier for each runner, which is used for logging
	identifier string
	store      storage
	handler    TaskHandler
	taskCh     chan *kv.KeyRange
	wg         *sync.WaitGroup

	err error

	completedRegions *int32
	failedRegions    *int32
}

// run starts the worker. It collects all objects from `w.taskCh` and process them one by one.
func (w *rangeTaskWorker) run(ctx context.Context, cancel context.CancelFunc) {
	defer w.wg.Done()
	for r := range w.taskCh {
		select {
		case <-ctx.Done():
			w.err = ctx.Err()
			return
		default:
		}

		stat, err := w.handler(ctx, *r)

		atomic.AddInt32(w.completedRegions, int32(stat.CompletedRegions))
		atomic.AddInt32(w.failedRegions, int32(stat.FailedRegions))
		metrics.TiKVRangeTaskStats.WithLabelValues(w.name, lblCompletedRegions).Add(float64(stat.CompletedRegions))
		metrics.TiKVRangeTaskStats.WithLabelValues(w.name, lblFailedRegions).Add(float64(stat.FailedRegions))

		if err != nil {
			logutil.Logger(ctx).Info("canceling range task because of error",
				zap.String("name", w.identifier),
				zap.String("startKey", redact.Key(r.StartKey)),
				zap.String("endKey", redact.Key(r.EndKey)),
				zap.Error(err))
			w.err = err
			cancel()
			break
		}
	}
}
