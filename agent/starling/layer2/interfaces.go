package layer2

//go:generate go tool mockgen -destination=../testhelpers/mock_l2_announcer.go -package=testhelpers -mock_names=Announcer=MockL2Announcer . Announcer

type Announcer interface {
	SetBalancer(name string, adv IPAdvertisement)
	DeleteBalancer(name string)
}
