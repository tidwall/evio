// Copyright 2018 Ryan Liu. All rights reserved.
// A session interface with id of a string

/*
Example:

package main

import (
	"errors"
	"fmt"
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
	uid     string
	SessId  string
	// ... Add other data
}

// Create empty session
func NewSession() *Session {
	return &Session{SessId:RandomGUID()}
}

func (sess *Session) GetId() string {
	return sess.uid
}

func (sess *Session) SetId(uid string) {
	sess.uid = uid
}

func GetClientId(in []byte) (uid string) {
	// if len(in) > 15 {
	//     uid = string(in[:15])
	// } else {
	//     uid = string(in)
	// }
	return
}

func GetSessAndId(c evio.Conn) (sess *Session, uid string) {
	if cxt := evio.GetSession(c); cxt != nil {
		sess = cxt.(*Session)
		uid = sess.GetId()
	}
	return
}

func WakeupConn(uid string) (err error) {
	c := evio.FindConnById(uid)
	if c == nil {
		err = errors.New(fmt.Sprintf("Connection %s is closed", uid))
		return
	}
	sess, _ := GetSessAndId(c)
	// Store other data for send
	// sess.xxx = ...
	evio.SaveSession(c, sess)
	c.Wake() // send data
	return
}

func main() {
	var events = evio.Events{}
	events.Opened = func(c evio.Conn) (out []byte, opts evio.Options, action evio.Action) {
		sess := NewSession()
		evio.BindSession(c, sess)
	}
	events.Data = func(c evio.Conn, in []byte) (out []byte, action evio.Action) {
		sess, uid := GetSessAndId(c)
		if sess == nil {
			sess = NewSession()
		}
		if in == nil { // Send, call by Wake()
			// out = sess.xxx
		} else if uid == "" { // Receive, first time
			if uid = GetClientId(in); uid != "" {
				sess.SetId(uid)
				evio.BindSession(c, sess)
			}
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

// Get session of current connection
func GetSession(c Conn) interface{} {
	if c == nil {
		return nil
	}
	return c.Context()
}

// Get only the id of session
func GetSessionId(cxt interface{}) string {
	if cxt == nil {
		return ""
	}
	if sess := cxt.(ISession); sess != nil {
		return sess.GetId()
	}
	return ""
}

// Save to the context of connection, after changed session data
func SaveSession(c Conn, sess ISession) string {
	id := sess.GetId()
	if c == nil && id != "" {
		c = FindConnById(id)
	}
	if c != nil {
		c.SetContext(sess)
	}
	return id
}

// Create session with a connection, called by Events.Opened() usually
func BindSession(c Conn, sess ISession) (success bool) {
	if c == nil {
		return
	}
	cxt := GetSession(c)
	if id := GetSessionId(cxt); id != "" {
		registry.Delete(id)
	}
	if id := SaveSession(c, sess); id != "" {
		registry.Store(id, c)
		success = true
	}
	return
}

// Destroy session, called by Events.Closed() usually
func DestroySession(c Conn) (found bool) {
	cxt := GetSession(c)
	if cxt == nil {
		return
	}
	if id := GetSessionId(cxt); id != "" {
		registry.Delete(id)
		found = true
	}
	c.SetContext(nil)
	return
}
