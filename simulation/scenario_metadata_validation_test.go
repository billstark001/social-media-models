package simulation_test

import (
	"testing"

	"smp/dynamics"
	"smp/model"
	"smp/simulation"
)

func makeValidMetadata() *simulation.ScenarioMetadata {
	return &simulation.ScenarioMetadata{
		UniqueName:        "valid-name_01",
		DynamicsType:      simulation.DynamicsTypeHK,
		HKParams:          *dynamics.DefaultHKParams(),
		MaxSimulationStep: 100,
		RecsysFactoryType: "Random",
		NetworkType:       "Random",
		NodeCount:         20,
		NodeFollowCount:   5,
		SMPModelPureParams: model.SMPModelPureParams{
			RecsysCount:     10,
			PostRetainCount: 3,
		},
	}
}

func TestScenarioMetadataValidateAcceptsValidConfig(t *testing.T) {
	meta := makeValidMetadata()
	if err := meta.Validate(); err != nil {
		t.Fatalf("expected valid metadata, got error: %v", err)
	}
}

func TestScenarioMetadataValidateRejectsInvalidUniqueName(t *testing.T) {
	meta := makeValidMetadata()
	meta.UniqueName = "bad/name"

	if err := meta.Validate(); err == nil {
		t.Fatal("expected error for invalid unique name, got nil")
	}
}

func TestScenarioMetadataValidateRejectsInvalidModelParams(t *testing.T) {
	meta := makeValidMetadata()
	meta.RecsysCount = 0

	if err := meta.Validate(); err == nil {
		t.Fatal("expected error for invalid model params, got nil")
	}
}

func TestScenarioMetadataValidateRejectsInvalidNodeFollowCount(t *testing.T) {
	meta := makeValidMetadata()
	meta.NodeFollowCount = meta.NodeCount

	if err := meta.Validate(); err == nil {
		t.Fatal("expected error for invalid node_follow_count, got nil")
	}
}

func TestScenarioMetadataValidateRejectsInvalidDynamicsParams(t *testing.T) {
	meta := makeValidMetadata()
	meta.HKParams.RewiringRate = 1.2

	if err := meta.Validate(); err == nil {
		t.Fatal("expected error for invalid dynamics params, got nil")
	}
}

func TestScenarioMetadataValidateRejectsEmptyDynamicsType(t *testing.T) {
	meta := makeValidMetadata()
	meta.DynamicsType = ""

	if err := meta.Validate(); err == nil {
		t.Fatal("expected error for empty DynamicsType, got nil")
	}
}

func TestScenarioMetadataValidateAcceptsLargeTolerance(t *testing.T) {
	meta := makeValidMetadata()
	meta.HKParams.Tolerance = 10

	if err := meta.Validate(); err != nil {
		t.Fatalf("expected large tolerance to be accepted, got error: %v", err)
	}
}

func TestScenarioMetadataValidateRejectsInvalidRecsysForDynamics(t *testing.T) {
	meta := makeValidMetadata()
	meta.DynamicsType = simulation.DynamicsTypeVoter
	meta.RecsysFactoryType = "Opinion"

	if err := meta.Validate(); err == nil {
		t.Fatal("expected error for invalid recsys_factory_type, got nil")
	}
}
