package batch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"smp/model"

	"github.com/vmihailenco/msgpack/v5"
)

// Fixtures from commit 1cc68da preserve request identities and persistent
// encodings when optional observation fields are omitted.
func TestHEADProtocolHashes(t *testing.T) {
	for _, tc := range []struct {
		spec *ExperimentSpec
		want string
	}{
		{nil, "7b2a7d61c01bcc73e979570911f4ee4a3c70209dfc4fac81a65a7c648031339c"},
		{&ExperimentSpec{Mode: "fresh", Replicates: 1, ObserveAt: []int{0, 1}}, "3657ce12e495ffe4cc592cda662228c34e8b04bf74b0f0bcb355161d8421a6a8"},
	} {
		got, err := protocolSHA256(testMetadata("wire-fixture"), OutputOptions{}, tc.spec, "")
		if err != nil || got != tc.want {
			t.Fatalf("HEAD protocol identity changed: %q, %v", got, err)
		}
	}
}

func TestHEADCollectionEncoding(t *testing.T) {
	metadata := testMetadata("wire-fixture")
	for _, tc := range []struct {
		marshal func(any) ([]byte, error)
		want    string
	}{
		{json.Marshal, "81020571ff9ed9568daddc7d341abe091114f6fd150266a7a9869ab0a8dfac86"},
		{msgpack.Marshal, "7d69bd6253c339db0f64fb764b1fb452278a0e461a857df0b935a6c97daf34f0"},
	} {
		encoded, err := tc.marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(encoded)
		if hex.EncodeToString(digest[:]) != tc.want {
			t.Fatal("HEAD metadata encoding changed when details were disabled")
		}
	}
	encoded, err := msgpack.Marshal(model.ViewPostsEventBody[float64]{})
	if err != nil {
		t.Fatal(err)
	}
	const want = "84b24e65696768626f72436f6e636f7264616e74c0b24e65696768626f72446973636f7264616e74c0b55265636f6d6d656e646564436f6e636f7264616e74c0b55265636f6d6d656e646564446973636f7264616e74c0"
	if hex.EncodeToString(encoded) != want {
		t.Fatal("HEAD lightweight ViewPosts encoding changed")
	}
}
