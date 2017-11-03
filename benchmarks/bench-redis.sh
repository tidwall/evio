#!/bin/bash

#set -e

echo ""
echo "--- BENCH REDIS START ---"
echo ""

cd $(dirname "${BASH_SOURCE[0]}")
function cleanup {
    echo "--- BENCH REDIS DONE ---"
    kill $(jobs -rp)
    wait $(jobs -rp) 2>/dev/null
}
trap cleanup EXIT

mkdir -p bin
$(pkill redis-server || printf "")
$(pkill evio-redis-server || printf "")

function gobench {
    printf "\e[96m[%s]\e[0m\n" $1
    if [ "$3" != "" ]; then
        go build -o $2 $3
    fi
    GOMAXPROCS=1 $2 --port $4 &
    sleep 1
    echo "Sending pings, 50 connections, 1 packet pipeline"
    redis-benchmark -p $4 -t ping -q -P 1
    echo "Sending pings, 50 connections, 10 packet pipeline"
    redis-benchmark -p $4 -t ping -q -P 10
    echo "Sending pings, 50 connections, 20 packet pipeline"
    redis-benchmark -p $4 -t ping -q -P 20
    echo ""
}
gobench "real/redis" redis-server "" 6392
gobench "evio/redis" bin/evio-redis-server ../examples/redis-server/main.go 6393
