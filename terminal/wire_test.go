package terminal_test

import (
	"encoding/hex"
	"reflect"
	"testing"

	"smp/terminal"

	"github.com/vmihailenco/msgpack/v5"
)

// Fixture generated from classifier.go at commit 9b090ce with msgpack v5.4.1.
// The shared terminal types retain their original default MsgPack field names.
func TestHEADMsgpackEncoding(t *testing.T) {
	options := terminal.Options{Epsilon: .25, OccupiedMass: .01, MajorMass: .02, PositionResolution: .125, MassResolution: .0625}
	result := terminal.Result{
		Status: "absorbed", Category: "k2", KAll: 2, KMajor: 2,
		Components: []terminal.Component{{Minimum: -.5, Maximum: -.5, Mass: .5, Major: true}, {Minimum: .5, Maximum: .5, Mass: .5, Major: true}},
		Margins:    terminal.Margins{GapToEpsilon: .25, DiameterToEpsilon: .25, MassToMajorCutoff: .48},
	}
	for _, fixture := range []struct {
		name   string
		value  any
		target any
		wire   string
	}{
		{"options", options, &terminal.Options{}, "85a7457073696c6f6ecb3fd0000000000000ac4f636375706965644d617373cb3f847ae147ae147ba94d616a6f724d617373cb3f947ae147ae147bb2506f736974696f6e5265736f6c7574696f6ecb3fc0000000000000ae4d6173735265736f6c7574696f6ecb3fb0000000000000"},
		{"result", result, &terminal.Result{}, "86a6537461747573a86162736f72626564a843617465676f7279a26b32a44b416c6c02a64b4d616a6f7202aa436f6d706f6e656e74739284a74d696e696d756dcbbfe0000000000000a74d6178696d756dcbbfe0000000000000a44d617373cb3fe0000000000000a54d616a6f72c384a74d696e696d756dcb3fe0000000000000a74d6178696d756dcb3fe0000000000000a44d617373cb3fe0000000000000a54d616a6f72c3a74d617267696e7383ac476170546f457073696c6f6ecb3fd0000000000000b14469616d65746572546f457073696c6f6ecb3fd0000000000000b14d617373546f4d616a6f724375746f6666cb3fdeb851eb851eb8"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			data, err := msgpack.Marshal(fixture.value)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(data) != fixture.wire {
				t.Fatalf("HEAD encoding changed: %x", data)
			}
			head, err := hex.DecodeString(fixture.wire)
			if err != nil {
				t.Fatal(err)
			}
			if err := msgpack.Unmarshal(head, fixture.target); err != nil {
				t.Fatal(err)
			}
			if got := reflect.ValueOf(fixture.target).Elem().Interface(); !reflect.DeepEqual(got, fixture.value) {
				t.Fatalf("HEAD decode changed: %+v", got)
			}
		})
	}
}
