package wafproccessor

import (
	"strconv"

	"github.com/corazawaf/coraza/v3/types"
	"github.com/rs/zerolog"
)

// Matched values come from the client and can be as big as the body.
const maxLoggedValueLen = 256

// logTransaction counts every request, then writes one line for a request that matched rules with the log action.
func logTransaction(logger *zerolog.Logger, tx types.Transaction) {
	var loggedRules []types.MatchedRule
	for _, rule := range tx.MatchedRules() {
		if isLogged(rule) {
			loggedRules = append(loggedRules, rule)
		}
	}
	blocked := tx.IsInterrupted()

	result := "passed"
	switch {
	case blocked:
		result = "blocked"
	case len(loggedRules) > 0:
		result = "detected"
	}
	stats.requests.WithLabelValues(result).Inc()
	if len(loggedRules) == 0 {
		return
	}
	for _, rule := range loggedRules {
		stats.ruleMatches.WithLabelValues(strconv.Itoa(rule.Rule().ID()), result).Inc()
	}

	level := zerolog.InfoLevel
	if blocked {
		level = zerolog.WarnLevel
	}
	event := logger.WithLevel(level).
		Str("transaction_id", tx.ID()).
		Str("client_ip_address", loggedRules[0].ClientIPAddress()).
		Str("server_ip_address", loggedRules[0].ServerIPAddress()).
		Str("uri", truncate(loggedRules[0].URI())).
		Bool("blocked", blocked)
	if blocked {
		event.Int("status", tx.Interruption().Status)
	}
	// The sampler may drop the line, building the rules for nothing would cost as much as writing them.
	if event.Enabled() {
		rules := event.CreateArray()
		for _, rule := range loggedRules {
			rules.Dict(ruleDict(event, rule))
		}
		event.Array("rules", rules)
	}
	event.Msg("WAF rules matched")
}

func ruleDict(event *zerolog.Event, rule types.MatchedRule) *zerolog.Event {
	matchedDatas := event.CreateArray()
	for _, data := range rule.MatchedDatas() {
		matchedDatas.Dict(event.CreateDict().
			Str("variable", data.Variable().Name()).
			Str("key", data.Key()).
			Str("value", truncate(data.Value())).
			Str("message", data.Message()).
			Str("data", truncate(data.Data())).
			Int("chain_level", data.ChainLevel()))
	}

	metadata := rule.Rule()
	return event.CreateDict().
		Int("id", metadata.ID()).
		Str("message", rule.Message()).
		Str("data", truncate(rule.Data())).
		Bool("disruptive", rule.Disruptive()).
		Str("file", metadata.File()).
		Int("line", metadata.Line()).
		Str("revision", metadata.Revision()).
		Str("severity", metadata.Severity().String()).
		Str("version", metadata.Version()).
		Strs("tags", metadata.Tags()).
		Int("maturity", metadata.Maturity()).
		Int("accuracy", metadata.Accuracy()).
		Str("operator", metadata.Operator()).
		Int("rule_phase", int(metadata.Phase())).
		Str("raw", metadata.Raw()).
		Str("sec_mark", metadata.SecMark()).
		Array("matched_datas", matchedDatas)
}

// isLogged leaves out the matches of rules without the log action, like the SecActions of the CRS setup that match
// on every request. types.MatchedRule only gets Log() in coraza 4, its implementation already has it.
func isLogged(rule types.MatchedRule) bool {
	withLog, ok := rule.(interface{ Log() bool })
	if !ok {
		return rule.Message() != ""
	}
	return withLog.Log()
}

func truncate(value string) string {
	if len(value) <= maxLoggedValueLen {
		return value
	}
	return value[:maxLoggedValueLen] + "..."
}
