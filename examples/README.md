# `evio examples`

## echo-server

Runs on port 5000

```
go run examples/echo-server/main.go
```

Connect with telnet and start entering text.

```
telnet localhost 5000
```

## http-server

Runs on port 8080

```
go run examples/http-server/main.go
```

Browse to http://localhost:8080.
All requests print `Hello World!`.

## redis-server

Runs on port 6380

```
go run examples/redis-server/main.go
```

- `GET`, `SET`, `DEL`, `FLUSHDB`, `QUIT`, `PING`, `ECHO`, `SHUTDOWN` commands.  
- Compatible with the [redis-cli](https://redis.io/topics/rediscli) and [redis clients](https://redis.io/clients).
