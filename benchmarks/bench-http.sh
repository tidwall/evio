#!/bin/bash

#set -e

echo ""
echo "--- HTTP START ---"
echo ""

cd $(dirname "${BASH_SOURCE[0]}")
function cleanup {
    echo "--- HTTP DONE ---"
    kill $(jobs -rp)
    wait $(jobs -rp) 2>/dev/null
}
trap cleanup EXIT

mkdir -p bin
$(pkill net-http-server || printf "")
$(pkill fasthttp-server || printf "")
$(pkill iris-server || printf "")
$(pkill evio-http-server || printf "")

function gobench {
    printf "\e[96m[%s]\e[0m\n" $1
    if [ "$3" != "" ]; then
        go build -o $2 $3
    fi
    GOMAXPROCS=1 $2 --port $4 &
    sleep 1
    echo "Using 50 connections, 10 seconds"
    wrk -t1 -c50 -d10 http://127.0.0.1:$4
    echo ""
}

gobench "net/http" bin/net-http-server net-http-server/main.go 8081
gobench "iris" bin/iris-server iris-server/main.go 8082
gobench "fasthttp" bin/fasthttp-server fasthttp-server/main.go 8083
gobench "evio/http" bin/evio-http-server ../examples/http-server/main.go 8084
