// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package common

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/pingcap/dm/dm/command"
	"github.com/pingcap/dm/dm/config"
	"github.com/pingcap/dm/dm/pb"
	parserpkg "github.com/pingcap/dm/pkg/parser"
	"github.com/pingcap/dm/pkg/terror"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/pingcap/errors"
	"github.com/pingcap/parser"
	toolutils "github.com/pingcap/tidb-tools/pkg/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var (
	masterClient pb.MasterClient
	globalConfig = &Config{}
)

// InitUtils inits necessary dmctl utils
func InitUtils(cfg *Config) error {
	globalConfig = cfg
	return errors.Trace(InitClient(cfg.MasterAddr, cfg.Security))
}

// InitClient initializes dm-master client
func InitClient(addr string, securityCfg config.Security) error {
	tls, err := toolutils.NewTLS(securityCfg.SSLCA, securityCfg.SSLCert, securityCfg.SSLKey, "", securityCfg.CertAllowedCN)
	if err != nil {
		return terror.ErrCtlInvalidTLSCfg.Delegate(err)
	}

	conn, err := grpc.Dial(addr, tls.ToGRPCDialOption(), grpc.WithBackoffMaxDelay(3*time.Second), grpc.WithBlock(), grpc.WithTimeout(3*time.Second))
	if err != nil {
		return terror.ErrCtlGRPCCreateConn.AnnotateDelegate(err, "can't connect to %s", addr)
	}
	masterClient = pb.NewMasterClient(conn)
	return nil
}

// GlobalConfig returns global dmctl config
func GlobalConfig() *Config {
	return globalConfig
}

// MasterClient returns dm-master client
func MasterClient() pb.MasterClient {
	return masterClient
}

// PrintLines adds a wrap to support `\n` within `chzyer/readline`
func PrintLines(format string, a ...interface{}) {
	fmt.Println(fmt.Sprintf(format, a...))
}

// PrettyPrintResponse prints a PRC response prettily
func PrettyPrintResponse(resp proto.Message) {
	s, err := marshResponseToString(resp)
	if err != nil {
		PrintLines("%v", err)
	} else {
		fmt.Println(s)
	}
}

// PrettyPrintInterface prints an interface through encoding/json prettily
func PrettyPrintInterface(resp interface{}) {
	s, err := json.MarshalIndent(resp, "", "    ")
	if err != nil {
		PrintLines("%v", err)
	} else {
		fmt.Println(string(s))
	}
}

func marshResponseToString(resp proto.Message) (string, error) {
	// encoding/json does not support proto Enum well
	mar := jsonpb.Marshaler{EmitDefaults: true, Indent: "    "}
	s, err := mar.MarshalToString(resp)
	return s, errors.Trace(err)
}

// PrettyPrintResponseWithCheckTask prints a RPC response may contain response Msg with check-task's response prettily.
// check-task's response may contain json-string when checking fail in `detail` field.
// ugly code, but it is a little hard to refine this because needing to convert type.
func PrettyPrintResponseWithCheckTask(resp proto.Message, subStr string) bool {
	var (
		err          error
		found        bool
		replacedStr  string
		marshaledStr string
		placeholder  = "PLACEHOLDER"
	)
	switch chr := resp.(type) {
	case *pb.StartTaskResponse:
		if strings.Contains(chr.Msg, subStr) {
			found = true
			rawMsg := chr.Msg
			chr.Msg = placeholder // replace Msg with placeholder
			marshaledStr, err = marshResponseToString(chr)
			if err == nil {
				replacedStr = strings.Replace(marshaledStr, placeholder, rawMsg, 1)
			}
		}
	case *pb.UpdateTaskResponse:
		if strings.Contains(chr.Msg, subStr) {
			found = true
			rawMsg := chr.Msg
			chr.Msg = placeholder // replace Msg with placeholder
			marshaledStr, err = marshResponseToString(chr)
			if err == nil {
				replacedStr = strings.Replace(marshaledStr, placeholder, rawMsg, 1)
			}
		}
	case *pb.CheckTaskResponse:
		if strings.Contains(chr.Msg, subStr) {
			found = true
			rawMsg := chr.Msg
			chr.Msg = placeholder // replace Msg with placeholder
			marshaledStr, err = marshResponseToString(chr)
			if err == nil {
				replacedStr = strings.Replace(marshaledStr, placeholder, rawMsg, 1)
			}
		}

	default:
		return false
	}

	if !found {
		return found
	}

	if err != nil {
		PrintLines("%v", err)
	} else {
		// add indent to make it prettily.
		replacedStr = strings.Replace(replacedStr, "detail: {", "   \tdetail: {", 1)
		fmt.Println(replacedStr)
	}
	return found
}

// GetFileContent reads and returns file's content
func GetFileContent(fpath string) ([]byte, error) {
	content, err := ioutil.ReadFile(fpath)
	if err != nil {
		return nil, errors.Annotate(err, "error in get file content")
	}
	return content, nil
}

// GetSourceArgs extracts sources from cmd
func GetSourceArgs(cmd *cobra.Command) ([]string, error) {
	ret, err := cmd.Flags().GetStringSlice("source")
	if err != nil {
		PrintLines("error in parse `-s` / `--source`")
	}
	return ret, err
}

// ExtractSQLsFromArgs extract multiple sql from args.
func ExtractSQLsFromArgs(args []string) ([]string, error) {
	if len(args) <= 0 {
		return nil, errors.New("args is empty")
	}

	concat := strings.TrimSpace(strings.Join(args, " "))
	concat = command.TrimQuoteMark(concat)

	parser := parser.New()
	nodes, err := parserpkg.Parse(parser, concat, "", "")
	if err != nil {
		return nil, errors.Annotatef(err, "invalid sql '%s'", concat)
	}
	realSQLs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		realSQLs = append(realSQLs, node.Text())
	}
	if len(realSQLs) == 0 {
		return nil, errors.New("no valid SQLs")
	}

	return realSQLs, nil
}

// GetTaskNameFromArgOrFile tries to retrieve name from the file if arg is yaml-filename-like, otherwise returns arg directly
func GetTaskNameFromArgOrFile(arg string) string {
	if !(strings.HasSuffix(arg, ".yaml") || strings.HasSuffix(arg, ".yml")) {
		return arg
	}
	var (
		content []byte
		err     error
	)
	if content, err = GetFileContent(arg); err != nil {
		return arg
	}
	cfg := config.NewTaskConfig()
	if err := cfg.Decode(string(content)); err != nil {
		return arg
	}
	return cfg.Name
}
