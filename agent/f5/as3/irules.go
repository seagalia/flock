package as3

import (
	"fmt"
	"strconv"
	"strings"
)

// extAuthTemplate follows the contract of ExternalAuth: exactly 200 allows, another status below 500 is passed to the
// client, anything else (5xx, timeout, connection error, chunked or incomplete answer) is an error. The catch turns
// every failure of the exchange, a TCL error of connect, send or recv included, into that error. The content-length is
// read with scan since TCL 8.4 reads a number with a leading zero as octal, and no body is read after a HEAD request
// nor with a bodiless status (204, 304), whatever the content-length says.
const extAuthTemplate = `proc check {} {
    set flock_deadline [expr {[clock clicks -milliseconds] + <<timeout>>}]
    set flock_auth_path <<path>>
    set flock_proto "http"
    if { [PROFILE::exists clientssl] } {
        set flock_proto "https"
    }
    set flock_req "[HTTP::method] $flock_auth_path HTTP/1.1\r\nHost: [HTTP::host]\r\nConnection: close\r\nContent-Length: 0\r\n"
    append flock_req "X-Forwarded-Method: [HTTP::method]\r\nX-Forwarded-Host: [HTTP::host]\r\nX-Forwarded-Uri: [HTTP::uri]\r\n"
    append flock_req "X-Forwarded-Proto: $flock_proto\r\nX-Forwarded-For: [IP::client_addr]\r\n"
    foreach flock_name <<request_headers>> {
        foreach flock_value [HTTP::header values $flock_name] {
            append flock_req "$flock_name: $flock_value\r\n"
        }
    }
    append flock_req "\r\n"
    set flock_conn ""
    set flock_failed [catch {
        set flock_left [expr {$flock_deadline - [clock clicks -milliseconds]}]
        if { $flock_left <= 0 } {
            error "no time left to connect"
        }
        set flock_conn [connect -timeout $flock_left -status flock_cstat <<service>>]
        if { $flock_conn eq "" } {
            error "connect $flock_cstat"
        }
        set flock_left [expr {$flock_deadline - [clock clicks -milliseconds]}]
        if { $flock_left <= 0 } {
            error "connected too late to send"
        }
        send -timeout $flock_left -status flock_sstat $flock_conn $flock_req
        if { $flock_sstat ne "sent" } {
            error "send $flock_sstat"
        }
        set flock_head ""
        while { [string range $flock_head end-3 end] ne "\r\n\r\n" } {
            set flock_left [expr {$flock_deadline - [clock clicks -milliseconds]}]
            if { $flock_left <= 0 || [string length $flock_head] > 8192 } {
                error "answer headers not read in time"
            }
            set flock_byte [recv -timeout $flock_left -status flock_rstat 1 $flock_conn]
            if { $flock_byte eq "" } {
                error "answer headers $flock_rstat"
            }
            append flock_head $flock_byte
        }
        set flock_lines [split [string map [list "\r\n" "\n"] [string trimright $flock_head]] "\n"]
        if { ![regexp {^HTTP/1\.[01] ([1-9][0-9][0-9])} [lindex $flock_lines 0] -> flock_status] } {
            error "bad status line"
        }
        set flock_headers [list]
        set flock_length 0
        foreach flock_line [lrange $flock_lines 1 end] {
            set flock_colon [string first ":" $flock_line]
            if { $flock_colon < 1 } {
                error "bad header line"
            }
            set flock_name [string tolower [string trim [string range $flock_line 0 [expr {$flock_colon - 1}]]]]
            set flock_value [string trim [string map [list "\r" ""] [string range $flock_line [expr {$flock_colon + 1}] end]]]
            if { $flock_name eq "transfer-encoding" } {
                error "transfer-encoding answer"
            }
            if { $flock_name eq "content-length" } {
                if { ![string is digit -strict $flock_value] || [scan $flock_value %d flock_length] != 1 || $flock_length > 65536 } {
                    error "bad content-length"
                }
            }
            lappend flock_headers $flock_name $flock_value
        }
        if { [HTTP::method] eq "HEAD" || $flock_status == 204 || $flock_status == 304 } {
            set flock_length 0
        }
        set flock_body ""
        while { [string length $flock_body] < $flock_length } {
            set flock_left [expr {$flock_deadline - [clock clicks -milliseconds]}]
            if { $flock_left <= 0 } {
                error "answer body not read in time"
            }
            set flock_chunk [recv -timeout $flock_left -status flock_rstat [expr {$flock_length - [string length $flock_body]}] $flock_conn]
            if { $flock_chunk eq "" } {
                error "answer body $flock_rstat"
            }
            append flock_body $flock_chunk
        }
    } flock_err]
    if { $flock_conn ne "" } {
        catch { close $flock_conn }
    }
    if { !$flock_failed && ($flock_status >= 500 || $flock_status < 200) } {
        set flock_failed 1
        set flock_err "status $flock_status"
    }
    if { $flock_failed } {
        log local0.warn "flock external auth <<log_name>> failed: $flock_err"
<<on_error>>
    }
    if { $flock_status == 200 } {
        foreach {flock_name flock_value} $flock_headers {
            if { [lsearch -exact <<upstream_headers>> $flock_name] >= 0 } {
                HTTP::header insert $flock_name $flock_value
            }
        }
        return 1
    }
    set flock_answer [list]
    foreach {flock_name flock_value} $flock_headers {
        if { [lsearch -exact <<client_headers>> $flock_name] >= 0 } {
            lappend flock_answer $flock_name $flock_value
        }
    }
    eval [list HTTP::respond $flock_status content $flock_body] $flock_answer
    return 0
}
`

// proxyRecvIRule reads the proxy protocol header (v1 text or v2 binary) a client sends first, removes it before the
// BIG-IP parses the stream, and rejects a connection without it, as flock's accept_proxy_protocol does.
const proxyRecvIRule = `when CLIENT_ACCEPTED {
    TCP::collect
}
when CLIENT_DATA {
` + proxyRecvBody + `    TCP::release
}
`

// proxyRecvBody strips the proxy protocol header from the collected payload, it is shared by the SNI iRule which
// must strip it before reading the ClientHello.
const proxyRecvBody = `    if { ![info exists flock_pp_done] } {
        set flock_pp_len 0
        if { [TCP::payload 6] eq "PROXY " } {
            set flock_pp_end [string first "\r\n" [TCP::payload]]
            if { $flock_pp_end < 0 } {
                TCP::collect
                return
            }
            set flock_pp_len [expr {$flock_pp_end + 2}]
        } elseif { [TCP::payload length] < 16 } {
            TCP::collect
            return
        } else {
            binary scan [TCP::payload 16] H24ccS flock_pp_sig flock_pp_ver flock_pp_fam flock_pp_addrlen
            if { $flock_pp_sig eq "0d0a0d0a000d0a515549540a" } {
                set flock_pp_len [expr {16 + ($flock_pp_addrlen & 0xffff)}]
                if { [TCP::payload length] < $flock_pp_len } {
                    TCP::collect
                    return
                }
            }
        }
        if { $flock_pp_len == 0 } {
            reject
            return
        }
        TCP::payload replace 0 $flock_pp_len ""
        set flock_pp_done 1
    }
`

// proxySendIRule sends a proxy protocol v1 header to the endpoint before the client data, as flock's
// enable_proxy_protocol does. An http or TLS virtual server reaches several pools: only the endpoints of the pool of
// the backend get the header.
func proxySendIRule(poolPath string) string {
	return `when SERVER_CONNECTED {
    if { [LB::server pool] eq ` + tclQuote(poolPath) + ` } {
        if { [IP::version] eq "6" } {
            set flock_pp_proto "TCP6"
        } else {
            set flock_pp_proto "TCP4"
        }
        TCP::respond "PROXY $flock_pp_proto [IP::client_addr] [IP::local_addr clientside] [TCP::client_port] [TCP::local_port clientside]\r\n"
    }
}
`
}

type extAuthParams struct {
	ServicePath     string
	Path            string
	TimeoutMs       int
	RequestHeaders  []string
	UpstreamHeaders []string
	ClientHeaders   []string
	FailOpen        bool
	StatusOnError   uint32
	LogName         string
}

// extAuthIRule holds the proc running the external auth check of one policy, called by the routing iRule before the
// action of a checked route. It answers the client itself on a deny or a denied error and returns whether the request
// goes on. The answer is read one byte at a time up to the end of its headers: recv waits for the count it is given,
// and only the content-length tells how much the body holds. The sideband connection is closed after each check, a
// kept one could have been closed by the auth service by the next request and turn it into an error.
func extAuthIRule(p extAuthParams) string {
	onError := fmt.Sprintf("HTTP::respond %d content \"\"\nreturn 0\n", p.StatusOnError)
	if p.FailOpen {
		onError = "return 1\n"
	}
	return strings.NewReplacer(
		"<<timeout>>", strconv.Itoa(p.TimeoutMs),
		"<<path>>", tclQuote(p.Path),
		"<<service>>", tclQuote(p.ServicePath),
		"<<request_headers>>", tclList(p.RequestHeaders),
		"<<upstream_headers>>", tclList(p.UpstreamHeaders),
		"<<client_headers>>", tclList(p.ClientHeaders),
		"<<log_name>>", tclEscape(p.LogName),
		"<<on_error>>\n", indent(onError, "        "),
	).Replace(extAuthTemplate)
}

func tclList(items []string) string {
	quoted := make([]string, 0, len(items)+1)
	quoted = append(quoted, "[list")
	for _, item := range items {
		quoted = append(quoted, tclQuote(item))
	}
	return strings.Join(quoted, " ") + "]"
}
