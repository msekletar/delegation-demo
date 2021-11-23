package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"
)

const (
	slice = "workload.slice"
	scope = "app.scope"
)

func main() {
	// Start scope with delegation enabled.
	manager, err := dbus.NewWithContext(context.Background())
	if err != nil {
		log.Fatalf("failed to establish bus connection to systemd: %v", err)
	}

	scopeProperties := []dbus.Property{
		dbus.PropPids(uint32(os.Getpid())),
		dbus.PropSlice(slice),
		{
			Name:  "CPUAccounting",
			Value: godbus.MakeVariant(true),
		},
		{
			Name:  "IOAccounting",
			Value: godbus.MakeVariant(true),
		},
		{
			Name:  "MemoryAccounting",
			Value: godbus.MakeVariant(true),
		},
		{
			Name:  "Delegate",
			Value: godbus.MakeVariant(true),
		},
	}

	jobCh := make(chan string)
	id, err := manager.StartTransientUnitContext(context.Background(), scope, "replace", scopeProperties, jobCh)
	if err != nil {
		log.Fatalf("failed to start %v: %v", scope, err)
	}

	job := <-jobCh
	if job != "done" {
		log.Fatalf("failed to start %v, job (id: %v) result is: %v", scope, id, job)
	}

	// Start dummy workload.
	workload := exec.Command("sleep", "infinity")
	workload.Start()

	// Setup two sub-cgroups in the scope's cgroup.
	scopeCg := fmt.Sprintf("/sys/fs/cgroup/%v/%v", slice, scope)

	managerCg := scopeCg + "/manager"
	if err := os.Mkdir(managerCg, 0755); err != nil {
		log.Fatalf("failed to create manager sub-cgroup: %v", err)
	}

	workerCg := scopeCg + "/worker"
	if err := os.Mkdir(workerCg, 0755); err != nil {
		log.Fatalf("failed to create worker sub-cgroup: %v", err)
	}

	// Migrate ourselves into manager sub-cgroup and workload in worker sub-cgroup.
	err = os.WriteFile(workerCg+"/cgroup.procs", []byte(fmt.Sprintln(workload.Process.Pid)), 0644)
	if err != nil {
		log.Fatalf("failed to migrate workload (PID %v) to cgroup %v: %v", workload.Process.Pid, workerCg, err)
	}

	err = os.WriteFile(managerCg+"/cgroup.procs", []byte(fmt.Sprintln(os.Getpid())), 0644)
	if err != nil {
		log.Fatalf("failed to migrate manager (PID %v) to cgroup %v: %v", workload.Process.Pid, workerCg, err)
	}
	// Scope cgroup is now empty and we have processes in leaves nodes so we should be able to make use delegate controllers.
	err = os.WriteFile(scopeCg+"/cgroup.subtree_control", []byte(fmt.Sprintln("+cpu +memory +io")), 0644)
	if err != nil {
		log.Fatalf("failed to enable delegated cgroup controller: %v", err)
	}

	time.Sleep(1 * time.Minute)
	workload.Process.Kill()
}
