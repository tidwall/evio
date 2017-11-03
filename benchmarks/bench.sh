#!/bin/bash

#set -e

cd $(dirname "${BASH_SOURCE[0]}")

./bench-http.sh
./bench-echo.sh
./bench-redis.sh
