package main

// The receiver classifier (aae-orc-tk8ge). Red-test stub: every message is
// routine, so the tests fail on their assertions.

import "time"

type msgClass string

const (
	classAct     msgClass = "act"
	classRoutine msgClass = "routine"
)

type classifier struct{ decodes int }

func newClassifier() *classifier { return &classifier{} }

func (c *classifier) size() int { return 0 }

func (c *classifier) classify(tier string, seq uint64, data []byte, now time.Time) msgClass {
	return classRoutine
}

func (c *classifier) noteSent(id, perf string)                {}
func (c *classifier) setBacklogFloor(tier string, seq uint64) {}
func (c *classifier) isBacklog(tier string, seq uint64) bool  { return false }
