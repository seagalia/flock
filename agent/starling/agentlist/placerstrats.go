package agentlist

import (
	"github.com/cespare/xxhash/v2"
	"github.com/hashicorp/memberlist"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"strings"
)

const DefaultPlacerStrat = "distributed"

var availablePlacerStrats = map[string]PlacerStrat{}

func init() {
	availablePlacerStrats["distributed"] = &Distributed{}
	availablePlacerStrats["longest_uptime"] = &LongestUptime{}
	availablePlacerStrats["shortest_uptime"] = &ShortestUptime{}
}

// GetPlacerStrat returns a PlacerStrat by name. If the name is empty, it will return the default PlacerStrat.
func GetPlacerStrat(name string) (PlacerStrat, bool) {
	if name == "" {
		name = DefaultPlacerStrat
	}
	name = strings.ToLower(name)
	strategy, ok := availablePlacerStrats[name]
	return strategy, ok
}

// GetPlacerStrats returns a list of available PlacerStrats.
func GetPlacerStrats() []string {
	keys := make([]string, 0, len(availablePlacerStrats))
	for k := range availablePlacerStrats {
		keys = append(keys, k)
	}
	return keys
}

type PlacerStrat interface {
	Placing([]*memberlist.Node, map[string][]*vip.VIP, *vip.VIP)
	Name() string
}

func hash(nodeName string, aVip *vip.VIP) uint64 {
	hash := xxhash.New()
	hash.WriteString(nodeName)                // nolint
	hash.WriteString(aVip.ToIpNet().String()) // nolint
	return hash.Sum64()
}

type Distributed struct{}

func (s *Distributed) Placing(nodes []*memberlist.Node, mapMemberVips map[string][]*vip.VIP, aVip *vip.VIP) {
	var minVal uint64
	var minUuid string
	for nodeName := range mapMemberVips {
		var sum uint64
		sum += hash(nodeName, aVip)
		if minVal == 0 || sum < minVal {
			minVal = sum
			minUuid = nodeName
		}
	}
	mapMemberVips[minUuid] = append(mapMemberVips[minUuid], aVip)
}

func (s *Distributed) Name() string {
	return "distributed"
}

type LongestUptime struct{}

func (s *LongestUptime) Placing(nodes []*memberlist.Node, mapMemberVips map[string][]*vip.VIP, aVip *vip.VIP) {
	if len(nodes) == 0 {
		fallbackVipPlacing(mapMemberVips, aVip)
		return
	}
	finalNode := nodes[0]
	for _, node := range nodes {
		if NodeUptime(node) > NodeUptime(finalNode) {
			finalNode = node
		}
	}
	mapMemberVips[finalNode.Name] = append(mapMemberVips[finalNode.Name], aVip)
}

func (s *LongestUptime) Name() string {
	return "longest_uptime"
}

type ShortestUptime struct{}

func (s *ShortestUptime) Placing(nodes []*memberlist.Node, mapMemberVips map[string][]*vip.VIP, aVip *vip.VIP) {
	if len(nodes) == 0 {
		fallbackVipPlacing(mapMemberVips, aVip)
		return
	}
	finalNode := nodes[0]
	for _, node := range nodes {
		if NodeUptime(node) < NodeUptime(finalNode) {
			finalNode = node
		}
	}
	mapMemberVips[finalNode.Name] = append(mapMemberVips[finalNode.Name], aVip)
}

func (s *ShortestUptime) Name() string {
	return "shortest_uptime"
}

func fallbackVipPlacing(mapMemberVips map[string][]*vip.VIP, aVip *vip.VIP) {
	finalNodeName := ""
	for nodeName := range mapMemberVips {
		finalNodeName = nodeName
		break
	}
	mapMemberVips[finalNodeName] = append(mapMemberVips[finalNodeName], aVip)
}
