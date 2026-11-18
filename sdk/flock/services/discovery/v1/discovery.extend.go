package svcdiscovery

import (
	"fmt"

	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func NewResourceFromMessage(msg proto.Message) (*Resource, error) {
	sig, err := helpers.MessageSignature(msg)
	if err != nil {
		return nil, err
	}
	anyRes, err := anypb.New(msg)
	if err != nil {
		return nil, err
	}
	return &Resource{
		Uuid:      helpers.GetIdentifier(msg),
		Signature: sig,
		Resource:  anyRes,
	}, nil
}

func (x *Resource) ToMessage(msg proto.Message) error {
	if x.GetResource() == nil {
		return nil
	}
	return x.GetResource().UnmarshalTo(msg)
}

func (x *Resource) StoredMessage() (proto.Message, error) {
	if x.GetResource() == nil {
		return nil, nil
	}
	return x.GetResource().UnmarshalNew()
}

func (x *DeltaDiscoveryResponse) LogSummary() string {
	uuids := make([]string, 0, len(x.GetResources()))
	for _, resource := range x.GetResources() {
		uuids = append(uuids, resource.GetUuid())
	}
	return fmt.Sprintf("nonce=%s resources=%v removed=%v", x.GetResponseNonce(), uuids, x.GetRemovedResources())
}
