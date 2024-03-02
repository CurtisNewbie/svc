package svc

import "log"

type Logger interface {
	Info(args ...any)
	Infof(pat string, args ...any)
	Error(args ...any)
	Errorf(pat string, args ...any)
}

type PrintLogger struct {
}

func (pl PrintLogger) Info(args ...any) {
	log.Print(args...)
}

func (pl PrintLogger) Infof(pat string, args ...any) {
	log.Printf(pat, args...)
}

func (pl PrintLogger) Error(args ...any) {
	log.Print(args...)
}

func (pl PrintLogger) Errorf(pat string, args ...any) {
	log.Printf(pat, args...)
}
