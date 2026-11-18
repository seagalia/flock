package core

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
)

func (x *URL) Format() string {
	if x == nil {
		return ""
	}
	host := x.GetHost()
	if x.GetPort() != 0 {
		host = net.JoinHostPort(host, strconv.Itoa(int(x.GetPort())))
	}
	return fmt.Sprintf("%s://%s%s", x.GetScheme(), host, x.GetPath())
}

func (x *URL) ToUrl() *url.URL {
	if x == nil {
		return nil
	}
	host := x.GetHost()
	port := strconv.Itoa(int(x.GetPort()))
	if x.GetPort() != 0 {
		host = net.JoinHostPort(host, port)
	}
	// the password goes as basic auth, a url string carrying it could reach a log line
	var userInfo *url.Userinfo
	if x.GetUserInfo() != nil {
		userInfo = url.User(x.GetUserInfo().GetUser())
	}
	return &url.URL{
		Scheme: x.GetScheme(),
		User:   userInfo,
		Host:   host,
		Path:   x.GetPath(),
	}
}

func UrlToCoreUrl(rawUrl string) (*URL, error) {
	u, err := url.Parse(rawUrl)
	if err != nil {
		return nil, err
	}
	var userInfo *URL_UserInfo
	// a password can only come from a secret, so the one of a raw url is dropped
	if u.User != nil {
		userInfo = &URL_UserInfo{User: u.User.Username()}
	}
	port := uint32(0)
	host := u.Host
	hostSplit, portSplit, err := net.SplitHostPort(u.Host)
	if err == nil {
		host = hostSplit
		portParsed, err := strconv.ParseUint(portSplit, 10, 32)
		if err != nil {
			return nil, err
		}
		port = uint32(portParsed)
	}
	return &URL{
		Scheme:   u.Scheme,
		UserInfo: userInfo,
		Host:     host,
		Port:     port,
		Path:     u.Path,
	}, nil
}
