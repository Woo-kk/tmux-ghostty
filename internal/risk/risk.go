package risk

import (
	"regexp"
	"strings"

	"github.com/guyuanshun/tmux-ghostty/internal/model"
)

var shellCombinerRE = regexp.MustCompile(`(\|\||&&|;|\||>>|>|<<|<|\n|\r|\$\(|` + "`" + `)`)

var readPrefixes = []string{
	"pwd",
	"ls",
	"cat",
	"head",
	"tail",
	"grep",
	"rg",
	"find",
	"ps",
	"kubectl get",
	"git status",
}

var navPrefixes = []string{
	"cd",
	"export",
	"alias",
	"source",
	"use",
}

var riskyPrefixes = []string{
	"rm",
	"mv",
	"cp",
	"sed -i",
	"tee",
	"truncate",
	"chmod",
	"chown",
	"systemctl",
	"service",
	"kubectl apply",
	"kubectl delete",
	"helm upgrade",
	"helm uninstall",
}

func Normalize(command string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
}

func Classify(command string) (string, model.RiskLevel) {
	normalized := Normalize(command)
	if normalized == "" {
		return "", model.RiskRisky
	}
	lower := strings.ToLower(normalized)

	if shellCombinerRE.MatchString(lower) {
		return normalized, model.RiskRisky
	}

	for _, prefix := range riskyPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return normalized, model.RiskRisky
		}
	}

	for _, prefix := range readPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix+" ") {
			return normalized, model.RiskRead
		}
	}

	for _, prefix := range navPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix+" ") {
			return normalized, model.RiskNav
		}
	}

	return normalized, model.RiskRisky
}
