package writer

import (
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/mfbonfigli/gotiler-core/tiler/model"
	"github.com/mfbonfigli/gotiler-core/tiler/plugin"
)

type Consumer interface {
	Consume(workchan chan *WorkUnit, errchan chan error, waitGroup *sync.WaitGroup)
}

type StandardConsumer struct {
	encoder plugin.GeometryEncoder
}

func NewStandardConsumer(optFn ...func(*StandardConsumer)) Consumer {
	c := &StandardConsumer{
		encoder: NewPntsEncoder("d.pnts", model.DefaultAttributes()),
	}
	for _, fn := range optFn {
		fn(c)
	}
	return c
}

// WithGeometryEncoder sets the consumer geometry encoder to the given one
func WithGeometryEncoder(e plugin.GeometryEncoder) func(*StandardConsumer) {
	return func(c *StandardConsumer) {
		c.encoder = e
	}
}

// Continually consumes WorkUnits submitted to a work channel producing corresponding gometry .pnts/.glb files and tileset.json files
// continues working until work channel is closed or if an error is raised. In this last case submits the error to an error
// channel before quitting
func (c *StandardConsumer) Consume(workchan chan *WorkUnit, errchan chan error, waitGroup *sync.WaitGroup) {
	defer func() {
		if r := recover(); r != nil {
			debug.PrintStack()
			errchan <- fmt.Errorf("panic: %v", r)
		}
	}()
	// signal waitgroup finished work
	defer waitGroup.Done()
	for {
		// get work from channel
		work, ok := <-workchan
		if !ok {
			// channel was closed by producer, quit infinite loop
			break
		}

		// do work
		err := c.doWork(work)

		// if there were errors during work send in error channel and quit
		if err != nil {
			errchan <- err
			break
		}
	}

}

// Takes a workunit and writes the corresponding content.glb/.pnts and tileset.json files
func (c *StandardConsumer) doWork(workUnit *WorkUnit) error {
	node := workUnit.Node

	// encodes and writes the geometries to the disk as a .pnts/.glb file
	err := c.encoder.Write(node, workUnit.WriterProvider, workUnit.Prefix)
	if err != nil {
		return err
	}
	if workUnit.OnDone != nil {
		workUnit.OnDone()
	}
	return nil
}
