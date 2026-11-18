package core

import "fmt"

func (x *SemanticVersion) Format() string {
	if x == nil {
		return ""
	}

	version := fmt.Sprintf("%d.%d.%d", x.GetMajor(), x.GetMinor(), x.GetPatch())
	if x.Prerelease != "" {
		version = fmt.Sprintf("%s-%s", version, x.Prerelease)
	}
	if x.Metadata != "" {
		version = fmt.Sprintf("%s+%s", version, x.Metadata)
	}
	return version
}

func HeaderValuesToMap(headerValues []*HeaderValue) map[string]string {
	headerValuesMap := make(map[string]string)
	for _, headerValue := range headerValues {
		headerValuesMap[headerValue.GetKey()] = headerValue.GetValue()
	}
	return headerValuesMap
}
