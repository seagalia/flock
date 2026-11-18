#!/usr/bin/env bash

modprobe ip_vs && echo "ip_vs" >/etc/modules-load.d/ipvs.conf
modprobe ip_vs_rr && echo "ip_vs_rr" >>/etc/modules-load.d/ipvs.conf
modprobe ip_vs_wrr && echo "ip_vs_wrr" >>/etc/modules-load.d/ipvs.conf
modprobe ip_vs_sh && echo "ip_vs_sh" >>/etc/modules-load.d/ipvs.conf
modprobe ip_vs_lc && echo "ip_vs_lc" >>/etc/modules-load.d/ipvs.conf
modprobe ip_vs_wlc && echo "ip_vs_wlc" >>/etc/modules-load.d/ipvs.conf
modprobe ip_vs_mh && echo "ip_vs_mh" >>/etc/modules-load.d/ipvs.conf
modprobe nf_conntrack && echo "nf_conntrack" >>/etc/modules-load.d/ipvs.conf

modprobe tcp_bbr && echo "tcp_bbr" >/etc/modules-load.d/bbr.conf
