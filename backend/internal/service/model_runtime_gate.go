package service

import (
	"context"
	"errors"
	"time"
)

const (
	defaultModelRuntimeQueueSize      = 128
	defaultModelRuntimeEnqueueTimeout = 5 * time.Second
)

type modelRuntimePriority int

const (
	modelRuntimePriorityHigh modelRuntimePriority = iota
	modelRuntimePriorityLow
)

var errModelRuntimeBusy = errors.New("model runtime is busy, please retry later")

var sharedModelRuntimeScheduler = newModelRuntimeScheduler(defaultModelRuntimeQueueSize, defaultModelRuntimeEnqueueTimeout)

type modelRuntimeJob struct {
	ctx      context.Context
	priority modelRuntimePriority
	fn       func(context.Context) error
	result   chan error
}

type modelRuntimeScheduler struct {
	highQueue      chan modelRuntimeJob
	lowQueue       chan modelRuntimeJob
	enqueueTimeout time.Duration
}

func newModelRuntimeScheduler(queueSize int, enqueueTimeout time.Duration) *modelRuntimeScheduler {
	if queueSize <= 0 {
		queueSize = defaultModelRuntimeQueueSize
	}
	if enqueueTimeout <= 0 {
		enqueueTimeout = defaultModelRuntimeEnqueueTimeout
	}

	scheduler := &modelRuntimeScheduler{
		highQueue:      make(chan modelRuntimeJob, queueSize),
		lowQueue:       make(chan modelRuntimeJob, queueSize),
		enqueueTimeout: enqueueTimeout,
	}
	go scheduler.loop()
	return scheduler
}

func (s *modelRuntimeScheduler) run(ctx context.Context, priority modelRuntimePriority, fn func(context.Context) error) error {
	if s == nil || fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	job := modelRuntimeJob{
		ctx:      ctx,
		priority: priority,
		fn:       fn,
		result:   make(chan error, 1),
	}

	queue := s.lowQueue
	if priority == modelRuntimePriorityHigh {
		queue = s.highQueue
	}

	timer := time.NewTimer(s.enqueueTimeout)
	defer timer.Stop()

	select {
	case queue <- job:
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errModelRuntimeBusy
	}

	select {
	case err := <-job.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *modelRuntimeScheduler) loop() {
	for {
		job := s.takeNextJob()
		if job.fn == nil {
			job.result <- nil
			continue
		}
		job.result <- job.fn(job.ctx)
	}
}

func (s *modelRuntimeScheduler) takeNextJob() modelRuntimeJob {
	for {
		select {
		case job := <-s.highQueue:
			return job
		default:
		}

		select {
		case job := <-s.highQueue:
			return job
		case job := <-s.lowQueue:
			return job
		}
	}
}
