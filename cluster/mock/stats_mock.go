// Package mock provides a variety of mock implementations used for testing.
/*
 * Copyright (c) 2018-2022, NVIDIA CORPORATION. All rights reserved.
 */
package mock

import (
	"github.com/NVIDIA/aistore/cluster"
	"github.com/NVIDIA/aistore/cmn/cos"
	"github.com/NVIDIA/aistore/stats"
)

type (
	StatsTracker struct{}
)

// interface guard
var _ stats.Tracker = (*StatsTracker)(nil)

func NewStatsTracker() stats.Tracker {
	return &StatsTracker{}
}

func (*StatsTracker) StartedUp() bool                  { return true }
func (*StatsTracker) Add(string, int64)                {}
func (*StatsTracker) Get(string) int64                 { return 0 }
func (*StatsTracker) AddErrorHTTP(string, int64)       {}
func (*StatsTracker) AddMany(...cos.NamedVal64)        {}
func (*StatsTracker) RegMetrics(*cluster.Snode)        {}
func (*StatsTracker) CoreStats() *stats.CoreStats      { return nil }
func (*StatsTracker) GetWhatStats() *stats.DaemonStats { return nil }
func (*StatsTracker) IsPrometheus() bool               { return false }
