package probe

import (
	"math"
	"testing"

	"smp/model"
)

func TestDeffuantZeroChangeAndSecondMoment(t *testing.T) {
	posts := []*model.PostRecord[float64]{{Opinion: 0}, {Opinion: 0.5}}
	second, zero := conditionalDeffuantMoments(0, posts, nil, driftParams{tolerance: 1, influence: 0.5})
	if math.Abs(second-0.03125) > 1e-12 || zero != 0.5 {
		t.Fatalf("second moment=%v, zero-change probability=%v", second, zero)
	}
	second, zero = conditionalDeffuantMoments(-1, posts, nil, driftParams{tolerance: 0.1, influence: 0.5})
	if second != 0 || zero != 1 {
		t.Fatalf("inactive second moment=%v, zero-change probability=%v", second, zero)
	}
}
