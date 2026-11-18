package challenges

import (
	"encoding/json"
	"strings"
)

const (
	MetadataKey    = "flock.acme.challenges"
	RefMetadataKey = "flock.acme.challenges.ref"
	MarkerKey      = "marker"
	markerValue    = "challenges"
	tokenKeyPrefix = "c."
)

// Challenge is the answer to one http-01 or tls-alpn-01 challenge of a domain.
type Challenge struct {
	Domain  string
	Token   string
	KeyAuth string
}

type challengeValue struct {
	Domain  string `json:"domain"`
	KeyAuth string `json:"key_auth"`
}

// EncodeData builds the data of the secret holding the challenges, one key per token.
func EncodeData(challenges []Challenge) map[string]string {
	data := map[string]string{MarkerKey: markerValue}
	for _, c := range challenges {
		// marshalling two strings can't fail
		value, _ := json.Marshal(challengeValue{Domain: c.Domain, KeyAuth: c.KeyAuth})
		data[tokenKeyPrefix+c.Token] = string(value)
	}
	return data
}

// DecodeData reads the challenges of a secret, skipping the marker and any key or value that doesn't decode.
func DecodeData(data map[string]string) []Challenge {
	var challenges []Challenge
	for key, raw := range data {
		token, isChallengeKey := strings.CutPrefix(key, tokenKeyPrefix)
		if !isChallengeKey || token == "" {
			continue
		}
		var value challengeValue
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			continue
		}
		challenges = append(challenges, Challenge{Domain: value.Domain, Token: token, KeyAuth: value.KeyAuth})
	}
	return challenges
}
