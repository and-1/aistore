// Package cos provides common low-level types and utilities for all aistore projects.
/*
 * Copyright (c) 2021, NVIDIA CORPORATION. All rights reserved.
 */
package cos

import (
	"time"

	jsoniter "github.com/json-iterator/go"
)

type Duration time.Duration // NOTE: the type name is used in iter-fields to parse

func (d Duration) D() time.Duration             { return time.Duration(d) }
func (d Duration) String() string               { return time.Duration(d).String() }
func (d Duration) MarshalJSON() ([]byte, error) { return jsoniter.Marshal(d.String()) }

func (d *Duration) UnmarshalJSON(b []byte) (err error) {
	var (
		dur time.Duration
		val string
	)
	if err = jsoniter.Unmarshal(b, &val); err != nil {
		return
	}
	dur, err = time.ParseDuration(val)
	*d = Duration(dur)
	return
}