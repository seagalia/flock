package starling_route

// DefaultWafRequestBodyLimit is the request_body_limit of a Waf that leaves it at 0.
const DefaultWafRequestBodyLimit = 64 << 10

func (x *Waf) ProvDefault() {
	if x == nil || x.GetRequestBodyLimit() != 0 {
		return
	}
	x.RequestBodyLimit = DefaultWafRequestBodyLimit
}
