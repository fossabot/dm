// Copyright 2020 PingCAP, Inc.
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

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/chaos-mesh/go-sqlsmith"
	"github.com/pingcap/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/pingcap/dm/dm/pb"
	"github.com/pingcap/dm/pkg/log"
)

// main starts to run the test case logic after MySQL, TiDB and DM have been set up.
// NOTE: run this in the same K8s namespace as DM-master.
func main() {
	cfg := newConfig()
	err := cfg.parse(os.Args[1:])
	switch errors.Cause(err) {
	case nil:
	case flag.ErrHelp:
		os.Exit(0)
	default:
		fmt.Println("parse cmd flags err:", err.Error())
		os.Exit(2)
	}

	err = log.InitLogger(&log.Config{Level: "info"})
	if err != nil {
		fmt.Println("init logger error:", err.Error())
		os.Exit(2)
	}

	go func() {
		http.ListenAndServe("0.0.0.0:80", nil) // for pprof
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sc := make(chan os.Signal, 1)
	signal.Notify(sc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		sig := <-sc
		log.L().Info("got signal to exit", zap.Stringer("signal", sig))
		cancel()
	}()

	ctx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel2()
	masterConn, err := grpc.DialContext(ctx2, cfg.MasterAddr, grpc.WithBlock(), grpc.WithInsecure()) // no TLS support in chaos cases now.
	if err != nil {
		log.L().Error("fail to dail DM-master", zap.String("address", cfg.MasterAddr), zap.Error(err))
		os.Exit(2)
	}
	masterCli := pb.NewMasterClient(masterConn)

	// check whether all members are ready.
	err = checkMembersReady(ctx2, masterCli) // ctx2, should be done in 60s.
	if err != nil {
		log.L().Error("fail to check members ready", zap.Error(err))
		os.Exit(2)
	}

	// create two sources.
	err = createSources(ctx, masterCli)
	if err != nil {
		log.L().Error("fail to create source", zap.Error(err))
		os.Exit(2)
	}
}
