## evio benchmark tools

Required tools:

- [wrk](https://github.com/wg/wrk) for HTTP
- [tcpkali](https://github.com/machinezone/tcpkali) for Echo
- [Redis](http://redis.io) for Redis


Required Go packages:

```
go get gonum.org/v1/plot/...
go get -u github.com/valyala/fasthttp
go get -u github.com/kataras/iris
```

And of course [Go](https://golang.org) is required.

Run `bench.sh` for all benchmarks.

