# `✨ shiny ✨ examples`

## redis-server

```
go run examples/redis-server/main.go [--port int] [--appendonly yes/no]
```

- `GET`, `SET`, `DEL`, `QUIT`, `PING`, `SHUTDOWN` commands.  
- `--appendonly yes` option for disk persistence.
- Compatible with the [redis-cli](https://redis.io/topics/rediscli) and all [redis clients](https://redis.io/clients).


## echo-server

```
go run examples/echo-server/main.go [--port int]
```
