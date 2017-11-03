#!/bin/bash

#set -e

echo ""
echo "--- BENCH ECHO START ---"
echo ""

cd $(dirname "${BASH_SOURCE[0]}")
function cleanup {
    echo "--- BENCH ECHO DONE ---"
    kill $(jobs -rp)
    wait $(jobs -rp) 2>/dev/null
}
trap cleanup EXIT

mkdir -p bin
$(pkill net-echo-server || printf "")
$(pkill evio-echo-server || printf "")

function gobench {
    printf "\e[96m[%s]\e[0m\n" $1
    if [ "$3" != "" ]; then
        go build -o $2 $3
    fi
    GOMAXPROCS=1 $2 --port $4 &
    sleep 1
    echo "Sending 6 byte packets, 50 connections"
    nl=$'\r\n'
    tcpkali -c 50 -m "PING{$nl}" 127.0.0.1:$4
    echo ""
}

gobench "net/echo" bin/net-echo-server net-echo-server/main.go 5001
gobench "evio/echo" bin/evio-echo-server ../examples/echo-server/main.go 5002
