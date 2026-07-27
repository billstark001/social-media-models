// Package rng provides reproducible, serializable random-number streams.
package rng

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
)

const (
	// AlgorithmPCG64DXSMV1 identifies the repository's PCG stream format.
	AlgorithmPCG64DXSMV1 = "pcg64-dxsm-v1"

	StreamNetwork        = "init-network"
	StreamOpinion        = "init-opinion"
	StreamSchedule       = "schedule"
	StreamDynamics       = "dynamics"
	StreamBehavior       = "behavior"
	StreamRecommendation = "recommendation"
)

var standardStreamNames = []string{
	StreamNetwork,
	StreamOpinion,
	StreamSchedule,
	StreamDynamics,
	StreamBehavior,
	StreamRecommendation,
}

// Spec is the portable root RNG parameter stored in metadata and finish marks.
// Seeds are hexadecimal strings so JSON consumers never lose uint64 precision.
type Spec struct {
	Algorithm string
	Seed1     string
	Seed2     string
}

// IsZero reports whether no RNG parameters were provided.
func (s Spec) IsZero() bool {
	return s.Algorithm == "" && s.Seed1 == "" && s.Seed2 == ""
}

// GenerateSpec creates a cryptographically seeded root RNG specification.
func GenerateSpec() (Spec, error) {
	var seed [16]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		return Spec{}, fmt.Errorf("generate RNG seed: %w", err)
	}
	return specFromUint64(
		binary.BigEndian.Uint64(seed[:8]),
		binary.BigEndian.Uint64(seed[8:]),
	), nil
}

// FixedSpec is convenient for tests and deterministic library callers.
func FixedSpec(seed1, seed2 uint64) Spec {
	return specFromUint64(seed1, seed2)
}

func specFromUint64(seed1, seed2 uint64) Spec {
	return Spec{
		Algorithm: AlgorithmPCG64DXSMV1,
		Seed1:     fmt.Sprintf("0x%016x", seed1),
		Seed2:     fmt.Sprintf("0x%016x", seed2),
	}
}

// Resolve validates a supplied specification or generates one when omitted.
func Resolve(spec Spec) (Spec, error) {
	if spec.IsZero() {
		return GenerateSpec()
	}
	seed1, seed2, err := spec.seeds()
	if err != nil {
		return Spec{}, err
	}
	return specFromUint64(seed1, seed2), nil
}

func parseSeed(name, value string) (uint64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("%s must not be empty", name)
	}
	seed, err := strconv.ParseUint(strings.TrimSpace(value), 0, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return seed, nil
}

func (s Spec) seeds() (uint64, uint64, error) {
	if s.Algorithm != AlgorithmPCG64DXSMV1 {
		return 0, 0, fmt.Errorf(
			"unsupported RNG algorithm %q (expected %q)",
			s.Algorithm,
			AlgorithmPCG64DXSMV1,
		)
	}
	seed1, err := parseSeed("RNG.Seed1", s.Seed1)
	if err != nil {
		return 0, 0, err
	}
	seed2, err := parseSeed("RNG.Seed2", s.Seed2)
	if err != nil {
		return 0, 0, err
	}
	return seed1, seed2, nil
}

type stream struct {
	source *rand.PCG
	rand   *rand.Rand
}

// Pool owns independent named streams derived from a root specification.
type Pool struct {
	spec    Spec
	streams map[string]*stream
}

// NewPool constructs all standard named streams.
func NewPool(spec Spec) (*Pool, error) {
	resolved, err := Resolve(spec)
	if err != nil {
		return nil, err
	}
	p := &Pool{
		spec:    resolved,
		streams: make(map[string]*stream, len(standardStreamNames)),
	}
	for _, name := range standardStreamNames {
		p.addDerivedStream(name)
	}
	return p, nil
}

// MustNewPool is intended for tests and static initialization.
func MustNewPool(spec Spec) *Pool {
	pool, err := NewPool(spec)
	if err != nil {
		panic(err)
	}
	return pool
}

// Spec returns the normalized portable root parameters.
func (p *Pool) Spec() Spec {
	return p.spec
}

func (p *Pool) addDerivedStream(name string) {
	seed1, seed2, err := p.spec.seeds()
	if err != nil {
		panic(err)
	}
	var root [16]byte
	binary.BigEndian.PutUint64(root[:8], seed1)
	binary.BigEndian.PutUint64(root[8:], seed2)
	h := sha256.New()
	h.Write(root[:])
	h.Write([]byte{0})
	h.Write([]byte(name))
	sum := h.Sum(nil)
	source := rand.NewPCG(
		binary.BigEndian.Uint64(sum[:8]),
		binary.BigEndian.Uint64(sum[8:16]),
	)
	p.streams[name] = &stream{source: source, rand: rand.New(source)}
}

// Stream returns a persistent named stream. Unknown names are derived lazily.
func (p *Pool) Stream(name string) *rand.Rand {
	if p == nil {
		panic("nil RNG pool")
	}
	s, ok := p.streams[name]
	if !ok {
		p.addDerivedStream(name)
		s = p.streams[name]
	}
	return s.rand
}

// Snapshot serializes the current state of every named PCG stream.
func (p *Pool) Snapshot() (map[string][]byte, error) {
	if p == nil {
		return nil, errors.New("nil RNG pool")
	}
	names := make([]string, 0, len(p.streams))
	for name := range p.streams {
		names = append(names, name)
	}
	sort.Strings(names)
	states := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := p.streams[name].source.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("marshal RNG stream %q: %w", name, err)
		}
		states[name] = data
	}
	return states, nil
}

// Restore replaces named stream states from a snapshot.
func (p *Pool) Restore(states map[string][]byte) error {
	if p == nil {
		return errors.New("nil RNG pool")
	}
	for name, data := range states {
		s, ok := p.streams[name]
		if !ok {
			p.addDerivedStream(name)
			s = p.streams[name]
		}
		if err := s.source.UnmarshalBinary(data); err != nil {
			return fmt.Errorf("restore RNG stream %q (%s): %w", name, hex.EncodeToString(data), err)
		}
	}
	return nil
}
