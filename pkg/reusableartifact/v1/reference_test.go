package reusableartifact

import "testing"

func TestSourceURIRoundTrip(t *testing.T) {
	want := Reference{MissionID: "mission_01", WorkUnitID: "work-unit_02", ArtifactID: "artifact_03"}
	encoded, err := SourceURI(want)
	if err != nil {
		t.Fatalf("SourceURI() error = %v", err)
	}
	got, err := ParseSourceURI(encoded)
	if err != nil {
		t.Fatalf("ParseSourceURI() error = %v", err)
	}
	if got != want {
		t.Fatalf("ParseSourceURI() = %#v, want %#v", got, want)
	}
}

func TestParseSourceURIRejectsAmbiguousOrUnsafeForms(t *testing.T) {
	valid := "agentx-artifact://scope/missions/mission_01/work-units/work_02/artifacts/artifact_03"
	tests := []string{
		"", "agentx-artifact://other/missions/mission_01/work-units/work_02/artifacts/artifact_03",
		valid + "?tenant=other", valid + "#fragment", valid + "/extra",
		"agentx-artifact://scope/missions/../work-units/work_02/artifacts/artifact_03",
		"agentx-artifact://scope/missions/mission%2Fother/work-units/work_02/artifacts/artifact_03",
		"agentx-artifact://user@scope/missions/mission_01/work-units/work_02/artifacts/artifact_03",
	}
	for _, value := range tests {
		if _, err := ParseSourceURI(value); err == nil {
			t.Errorf("ParseSourceURI(%q) succeeded, want rejection", value)
		}
	}
}
