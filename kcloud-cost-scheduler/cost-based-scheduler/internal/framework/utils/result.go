/*
Result types for scheduling plugin outputs

This module provides standard status codes and result wrappers that plugins
must return to communicate their decisions back to the framework engine.
*/
package utils

import (
	"errors"
	"math"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type PluginResultMap map[string]PluginResult

type PluginResult struct {
	AvailableGPUCount int
	IsFiltered        bool
	FilteredStatus    Status
	Scores            []PluginScore
	TotalNodeScore    int
	GPUScores         map[string]*GPUScore
	TotalGPUScore     int
	FinalScore        int
	BestGPU           string
}

type GPUScore struct {
	UUID           string
	IsFiltered     bool
	FilteredStatus Status
	GPUScore       int
	PodCount       int
	IsSelected     bool
}

type PluginScore struct {
	PluginName string
	Score      int64
}

func NewPluginResult() *PluginResult {
	return &PluginResult{
		AvailableGPUCount: 0,
		IsFiltered:        false,
		FilteredStatus:    Status{},
		TotalNodeScore:    0,
		GPUScores:         make(map[string]*GPUScore),
		TotalGPUScore:     0,
		FinalScore:        0,
		BestGPU:           "",
	}
}

func (pr *PluginResult) InitPluginResult() {
	pr.AvailableGPUCount = 0
	pr.IsFiltered = false
	pr.FilteredStatus = Status{}
	pr.TotalNodeScore = 0
	for uuid, gpuscore := range pr.GPUScores {
		gpuscore.InitGPUScore(uuid)
	}
	pr.TotalGPUScore = 0
	pr.FinalScore = 0
	pr.BestGPU = ""
}

func NewGPUScore(uuid string) *GPUScore {
	return &GPUScore{
		UUID:           uuid,
		IsFiltered:     false,
		FilteredStatus: Status{},
		GPUScore:       0,
		PodCount:       0,
		IsSelected:     false,
	}
}

func (gs *GPUScore) InitGPUScore(uuid string) {
	gs.UUID = uuid
	gs.GPUScore = 0
	gs.FilteredStatus = Status{}
	gs.IsFiltered = false
	gs.IsSelected = false
}

type Code int

const (
	Success Code = iota
	Error
	Unschedulable
	UnschedulableAndUnresolvable
	Wait
	Skip
	Pending
)

var codes = []string{"Success", "Error", "Unschedulable", "UnschedulableAndUnresolvable", "Wait", "Skip", "Pending"}

func (c Code) String() string {
	return codes[c]
}

const (
	MaxNodeScore  int64 = 100
	MinNodeScore  int64 = 0
	MaxTotalScore int64 = math.MaxInt64
)

type Status struct {
	code    Code
	reasons []string
	err     error
	plugin  string
}

func (s *Status) WithError(err error) *Status {
	s.err = err
	return s
}

func (s *Status) Code() Code {
	if s == nil {
		return Success
	}
	return s.code
}

func (s *Status) Message() string {
	if s == nil {
		return ""
	}
	return strings.Join(s.Reasons(), ", ")
}

func (s *Status) SetPlugin(plugin string) {
	s.plugin = plugin
}

func (s *Status) WithPlugin(plugin string) *Status {
	s.SetPlugin(plugin)
	return s
}

func (s *Status) Plugin() string {
	return s.plugin
}

func (s *Status) Reasons() []string {
	if s.err != nil {
		return append([]string{s.err.Error()}, s.reasons...)
	}
	return s.reasons
}

func (s *Status) AppendReason(reason string) {
	s.reasons = append(s.reasons, reason)
}

func (s *Status) IsSuccess() bool {
	return s.Code() == Success
}

func (s *Status) IsWait() bool {
	return s.Code() == Wait
}

func (s *Status) IsSkip() bool {
	return s.Code() == Skip
}

func (s *Status) IsRejected() bool {
	code := s.Code()
	return code == Unschedulable || code == UnschedulableAndUnresolvable || code == Pending
}

func (s *Status) AsError() error {
	if s.IsSuccess() || s.IsWait() || s.IsSkip() {
		return nil
	}
	if s.err != nil {
		return s.err
	}
	return errors.New(s.Message())
}

func (s *Status) Equal(x *Status) bool {
	if s == nil || x == nil {
		return s.IsSuccess() && x.IsSuccess()
	}
	if s.code != x.code {
		return false
	}
	if !cmp.Equal(s.err, x.err, cmpopts.EquateErrors()) {
		return false
	}
	if !cmp.Equal(s.reasons, x.reasons) {
		return false
	}
	return cmp.Equal(s.plugin, x.plugin)
}

func (s *Status) String() string {
	return s.Message()
}

func NewStatus(code Code, reasons ...string) *Status {
	s := &Status{
		code:    code,
		reasons: reasons,
	}
	return s
}

func AsStatus(err error) *Status {
	if err == nil {
		return nil
	}
	return &Status{
		code: Error,
		err:  err,
	}
}
