/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>

*/
package main

import (
	"ekvols/cmd"
	"ekvols/pkg/logger"
	"os"

	"go.uber.org/zap"
)

var Logger *zap.Logger

func main() {
	if err := cmd.Execute(); err != nil {
		// logger.Logger.Error("command failed", zap.Error(err))
		_ = logger.Logger.Sync()
		os.Exit(1)
	}
}
