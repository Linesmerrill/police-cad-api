package models

import "testing"

func TestRankRuleIsEmpty(t *testing.T) {
	cases := []struct {
		name string
		rule *RankRule
		want bool
	}{
		{"nil", nil, true},
		{"blank mode", &RankRule{Mode: "", AnchorRankIDs: []string{"a"}}, true},
		{"no anchors", &RankRule{Mode: "exact", AnchorRankIDs: nil}, true},
		{"populated", &RankRule{Mode: "exact", AnchorRankIDs: []string{"a"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rule.IsEmpty(); got != c.want {
				t.Fatalf("IsEmpty() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestEffectiveRankGates(t *testing.T) {
	rule := &RankRule{Mode: "atOrAbove", AnchorRankIDs: []string{"rank1"}}

	t.Run("prefers RankGates when present", func(t *testing.T) {
		d := FormTemplateDetails{
			RankGates: []DepartmentRankGate{{DepartmentID: "deptA", VisibleToRankRule: rule}},
			// Legacy fields set too — must be ignored in favor of RankGates.
			DepartmentID:      "deptLegacy",
			VisibleToRankRule: rule,
		}
		got := d.EffectiveRankGates()
		if len(got) != 1 || got[0].DepartmentID != "deptA" {
			t.Fatalf("expected the RankGates value, got %+v", got)
		}
	})

	t.Run("synthesizes a gate from legacy fields", func(t *testing.T) {
		d := FormTemplateDetails{
			DepartmentID:       "deptLegacy",
			VisibleToRankRule:  rule,
			EditableByRankRule: nil,
		}
		got := d.EffectiveRankGates()
		if len(got) != 1 {
			t.Fatalf("expected 1 synthesized gate, got %d", len(got))
		}
		if got[0].DepartmentID != "deptLegacy" || got[0].VisibleToRankRule != rule {
			t.Fatalf("synthesized gate mismatch: %+v", got[0])
		}
	})

	t.Run("legacy departmentId with no rule yields no gates", func(t *testing.T) {
		d := FormTemplateDetails{DepartmentID: "deptLegacy"}
		if got := d.EffectiveRankGates(); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("no rank gating at all yields nil", func(t *testing.T) {
		d := FormTemplateDetails{}
		if got := d.EffectiveRankGates(); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})
}
