package wafproccessor

type envoyMetadata map[string][]string

func (m envoyMetadata) Get(key string) string {
	if m == nil {
		return ""
	}
	v := m[key]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
