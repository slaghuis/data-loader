package reload

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Reloader is anything that can be asked to synchronise itself with metadata.
type Reloader interface {
	Reload(ctx context.Context) error
}

// Coordinator combines SIGHUP handling and periodic polling into one lifecycle.
type Coordinator struct {
	reloader Reloader
	logger   *slog.Logger

	interval    time.Duration
	handleHUP   bool

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type Config struct {
	Interval  time.Duration // 0 disables the periodic timer
	HandleHUP bool
}

func New(reloader Reloader, cfg Config, logger *slog.Logger) *Coordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Coordinator{
		reloader:  reloader,
		logger:    logger,
		interval:  cfg.Interval,
		handleHUP: cfg.HandleHUP,
		stopCh:    make(chan struct{}),
	}
}

func (c *Coordinator) Start() {
	if c.handleHUP {
		c.wg.Add(1)
		go c.sighupLoop()
	}
	if c.interval > 0 {
		c.wg.Add(1)
		go c.pollLoop()
	}
	c.logger.Info("reload coordinator started",
		"category", "schedule",
		"sighup", c.handleHUP,
		"interval", c.interval.String(),
	)
}

func (c *Coordinator) Stop() {
	close(c.stopCh)
	c.wg.Wait()
}

func (c *Coordinator) sighupLoop() {
	defer c.wg.Done()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)
	defer signal.Stop(sigs)

	for {
		select {
		case <-c.stopCh:
			return
		case <-sigs:
			c.logger.Info("SIGHUP received — reloading", "category", "schedule")
			c.doReload("sighup")
		}
	}
}

func (c *Coordinator) pollLoop() {
	defer c.wg.Done()
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-t.C:
			c.doReload("periodic")
		}
	}
}

func (c *Coordinator) doReload(source string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.reloader.Reload(ctx); err != nil {
		c.logger.Error("reload failed",
			"category", "schedule",
			"trigger", source,
			"err", err,
		)
	}
}