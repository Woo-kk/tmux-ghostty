package risk

import (
	"testing"

	"github.com/Woo-kk/tmux-ghostty/internal/model"
)

func TestClassify(t *testing.T) {
	testCases := []struct {
		command string
		stage   model.PaneStage
		want    model.RiskLevel
	}{
		{command: "pwd", want: model.RiskRead},
		{command: "git status -sb", want: model.RiskRead},
		{command: "cd /tmp", want: model.RiskNav},
		{command: "kubectl apply -f k8s.yaml", want: model.RiskRisky},
		{command: "echo hi > file.txt", want: model.RiskRisky},
		{command: "2801", stage: model.StageMenu, want: model.RiskNav},
		{command: "/2801", stage: model.StageTargetSearch, want: model.RiskNav},
		{command: "1", stage: model.StageSelection, want: model.RiskNav},
		{command: "h", stage: model.StageMenu, want: model.RiskNav},
		{command: "1", stage: model.StageShell, want: model.RiskRisky},
		{command: "unknowncmd", want: model.RiskRisky},
	}

	for _, testCase := range testCases {
		_, got := Classify(testCase.command, Context{Stage: testCase.stage})
		if got != testCase.want {
			t.Fatalf("classify(%q) = %q, want %q", testCase.command, got, testCase.want)
		}
	}
}
