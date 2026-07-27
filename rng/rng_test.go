package rng_test

import (
	"reflect"
	"testing"

	smprng "smp/rng"
)

func TestFixedSpecReproducesNamedStreams(t *testing.T) {
	a := smprng.MustNewPool(smprng.FixedSpec(1, 2))
	b := smprng.MustNewPool(smprng.FixedSpec(1, 2))
	for _, name := range []string{
		smprng.StreamNetwork,
		smprng.StreamOpinion,
		smprng.StreamSchedule,
		smprng.StreamDynamics,
		smprng.StreamBehavior,
		smprng.StreamRecommendation,
	} {
		for range 16 {
			if got, want := a.Stream(name).Uint64(), b.Stream(name).Uint64(); got != want {
				t.Fatalf("stream %q differs: got %d, want %d", name, got, want)
			}
		}
	}
}

func TestPoolSnapshotRestore(t *testing.T) {
	original := smprng.MustNewPool(smprng.FixedSpec(3, 4))
	stream := original.Stream(smprng.StreamRecommendation)
	for range 7 {
		stream.Uint64()
	}
	state, err := original.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	want := make([]uint64, 10)
	for i := range want {
		want[i] = stream.Uint64()
	}

	restored := smprng.MustNewPool(smprng.FixedSpec(3, 4))
	if err := restored.Restore(state); err != nil {
		t.Fatal(err)
	}
	got := make([]uint64, 10)
	for i := range got {
		got[i] = restored.Stream(smprng.StreamRecommendation).Uint64()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored sequence differs:\ngot  %v\nwant %v", got, want)
	}
}

func TestResolveNormalizesSeeds(t *testing.T) {
	spec, err := smprng.Resolve(smprng.Spec{
		Algorithm: smprng.AlgorithmPCG64DXSMV1,
		Seed1:     "0x1",
		Seed2:     "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Seed1 != "0x0000000000000001" || spec.Seed2 != "0x0000000000000002" {
		t.Fatalf("unexpected normalized spec: %+v", spec)
	}
}
