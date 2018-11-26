// Copyright 2018 Ryan Liu. All rights reserved.
// A session interface with id of a string

/*
Example:

package main

import (
	"math/rand"
	"time"

	"github.com/oklog/ulid"
	"github.com/tidwall/evio"
)

// A creator of session id
func RandomGUID() string {
	now := time.Now()
	source := rand.NewSource(now.UnixNano())
	entropy := ulid.Monotonic(rand.New(source), 0)
	randId := ulid.MustNew(ulid.Timestamp(now), entropy)
	return randId.String()
}

type Session struct {
	id string
	// ... Add other data
}

// Create empty session
func NewSession() *Session {
	return &Session{id:RandomGUID()}
}

func (sess *Session) GetId() string {
	return sess.id
}

func (sess *Session) SetId(id string) {
	sess.id = id
}

func main() {
	var events = evio.Events{}
	events.Opened = func(c evio.Conn) (out []byte, opts evio.Options, action evio.Action) {
		sess := NewSession()
		evio.BindSession(c, sess)
	}
	events.Data = func(c evio.Conn, in []byte) (out []byte, action evio.Action) {
		if cxt := evio.GetSession(c); cxt != nil {
			sess := cxt.(*Session)
			// Store other data
			// sess.xxx = ...
			evio.SaveSession(sess)
		}
		return
	}
	events.Closed = func(c evio.Conn, err error) (action evio.Action) {
		evio.DestroySession(c)
		return
	}
}
*/

package evio

import "sync"

// Conn map, use session id as the key
var registry sync.Map

// A session interface
type ISession interface {
	GetId() string
	SetId(id string)
}

// Get connection
func FindConnById(id string) Conn {
	if c, ok := registry.Load(id); ok {
		return c.(Conn)
	}
	return nil
}

// Get connection and session id
func FindConn(sess ISession) (string, Conn) {
	if id := sess.GetId(); id != "" {
		return id, FindConnById(id)
	}
	return "", nil
}

// Get Session of current connection, called by Events.Receive() or Events.Closed() usually
func GetSession(c Conn) interface{} {
	return c.Context()
}

// Destroy Session, called by Events.Closed() usually
func DestroySession(c Conn) {
	if cxt := GetSession(c); cxt != nil {
		sess := cxt.(ISession)
		if id := sess.GetId(); id != "" {
			registry.Delete(id)
		}
		c.SetContext(nil)
	}
}

// Create Session with a connection, called by Events.Opened() usually
func BindSession(c Conn, sess ISession) {
	c.SetContext(sess)
	if id := sess.GetId(); id != "" {
		registry.Store(id, c)
	}
}

// Save to the context of connection, after changed session data
func SaveSession(sess ISession) bool {
	if _, c := FindConn(sess); c != nil {
		c.SetContext(sess)
		return true
	}
	return false
}
