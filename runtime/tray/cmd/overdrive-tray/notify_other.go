//go:build !linux && !darwin && !windows

package main

func notificationsPaused() bool { return false }

func sendNotice(string, string, string) error { return nil }
