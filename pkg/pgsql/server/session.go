/*
Copyright 2022 Codenotary Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"context"
	"crypto/tls"

	"net"

	"github.com/codenotary/immudb/embedded/logger"
	"github.com/codenotary/immudb/embedded/sql"
	"github.com/codenotary/immudb/pkg/client"
	"github.com/codenotary/immudb/pkg/database"
	"github.com/codenotary/immudb/pkg/pgsql/errors"
	fm "github.com/codenotary/immudb/pkg/pgsql/server/fmessages"
	"github.com/codenotary/immudb/pkg/pgsql/server/pgmeta"
)

type session struct {
	immudbHost string
	immudbPort int
	tlsConfig  *tls.Config
	log        logger.Logger

	dbList database.DatabaseList

	client client.ImmuClient

	ctx context.Context
	db  database.DB
	tx  *sql.SQLTx

	mr MessageReader

	connParams      map[string]string
	protocolVersion string

	statements map[string]*statement
	portals    map[string]*portal
}

type Session interface {
	InitializeSession() error
	HandleStartup(context.Context) error
	QueryMachine() error
	HandleError(error)
	Close() error
}

func newSession(c net.Conn, immudbHost string, immudbPort int,
	log logger.Logger, tlsConfig *tls.Config, dbList database.DatabaseList) *session {

	return &session{
		immudbHost: immudbHost,
		immudbPort: immudbPort,
		tlsConfig:  tlsConfig,
		log:        log,
		dbList:     dbList,
		mr:         NewMessageReader(c),
		statements: make(map[string]*statement),
		portals:    make(map[string]*portal),
	}
}

func (s *session) HandleError(e error) {
	pgerr := errors.MapPgError(e)

	_, err := s.writeMessage(pgerr.Encode())
	if err != nil {
		s.log.Errorf("unable to write error on wire: %w", err)
	}
}

func (s *session) nextMessage() (interface{}, bool, error) {
	msg, err := s.mr.ReadRawMessage()
	if err != nil {
		return nil, false, err
	}

	s.log.Debugf("received %s - %s message", string(msg.t), pgmeta.MTypes[msg.t])

	extQueryMode := false

	i, err := s.parseRawMessage(msg)
	if msg.t == 'P' ||
		msg.t == 'B' ||
		msg.t == 'D' ||
		msg.t == 'E' ||
		msg.t == 'H' {
		extQueryMode = true
	}

	return i, extQueryMode, err
}

func (s *session) parseRawMessage(msg *rawMessage) (interface{}, error) {
	switch msg.t {
	case 'p':
		return fm.ParsePasswordMsg(msg.payload)
	case 'Q':
		return fm.ParseQueryMsg(msg.payload)
	case 'X':
		return fm.ParseTerminateMsg(msg.payload)
	case 'P':
		return fm.ParseParseMsg(msg.payload)
	case 'B':
		return fm.ParseBindMsg(msg.payload)
	case 'D':
		return fm.ParseDescribeMsg(msg.payload)
	case 'S':
		return fm.ParseSyncMsg(msg.payload)
	case 'E':
		return fm.ParseExecuteMsg(msg.payload)
	case 'H':
		return fm.ParseFlushMsg(msg.payload)
	default:
		return nil, errors.ErrUnknowMessageType
	}
}

func (s *session) writeMessage(msg []byte) (int, error) {
	if len(msg) > 0 {
		s.log.Debugf("write %s - %s message", string(msg[0]), pgmeta.MTypes[msg[0]])
	}

	return s.mr.Write(msg)
}
