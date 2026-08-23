package server

import "syscall"

// Darwin names the keepalive idle-time option TCP_KEEPALIVE.
const tcpKeepaliveIdleOption = syscall.TCP_KEEPALIVE
