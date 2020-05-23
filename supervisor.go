package main

import (
	"fmt"
	"net/http"
	"os"
	"supervisord/config"
	"supervisord/faults"
	"supervisord/logger"
	"supervisord/process"
	"supervisord/signals"
	"supervisord/types"
	"supervisord/util"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	// SupervisorVersion the version of supervisor
	SupervisorVersion = "3.0"
)

// Supervisor manage all the processes defined in the supervisor configuration file.
// All the supervisor public interface is defined in this class
type Supervisor struct {
	config     *config.Config   // supervisor configuration
	procMgr    *process.Manager // process manager
	xmlRPC     *XMLRPC          // XMLRPC interface
	logger     logger.Logger    // logger manager
	restarting bool             // if supervisor is in restarting state
}

// StartProcessArgs arguments for starting a process
type StartProcessArgs struct {
	Name string // program name
	Wait bool   `default:"true"` // Wait the program starting finished
}

// ProcessStdin  process stdin from client
type ProcessStdin struct {
	Name  string // program name
	Chars string // inputs from client
}

// StateInfo describe the state of supervisor
type StateInfo struct {
	Statecode int    `xml:"statecode"`
	Statename string `xml:"statename"`
}

// RPCTaskResult result of some remote commands
type RPCTaskResult struct {
	Name        string `xml:"name"`        // the program name
	Group       string `xml:"group"`       // the group of the program
	Status      int    `xml:"status"`      // the status of the program
	Description string `xml:"description"` // the description of program
}

// LogReadInfo the input argument to read the log of supervisor
type LogReadInfo struct {
	Offset int // the log offset
	Length int // the length of log to read
}

// ProcessLogReadInfo the input argument to read the log of program
type ProcessLogReadInfo struct {
	Name   string // the program name
	Offset int    // the offset of the program log
	Length int    // the length of log to read
}

// ProcessTailLog the output of tail the program log
type ProcessTailLog struct {
	LogData  string
	Offset   int64
	Overflow bool
}

// NewSupervisor create a Supervisor object with supervisor configuration file
func NewSupervisor(configFile string) *Supervisor {
	return &Supervisor{
		config:     config.NewConfig(configFile),
		procMgr:    process.NewManager(),
		xmlRPC:     NewXMLRPC(),
		restarting: false,
	}
}

// GetVersion get the version of supervisor
func (s *Supervisor) GetVersion(r *http.Request, args *struct{}, reply *struct{ Version string }) error {
	reply.Version = SupervisorVersion
	return nil
}

// GetSupervisorVersion get the supervisor version
func (s *Supervisor) GetSupervisorVersion(r *http.Request, args *struct{}, reply *struct{ Version string }) error {
	reply.Version = SupervisorVersion
	return nil
}

// GetIdentification get the supervisor identifier configured in the file
func (s *Supervisor) GetIdentification(r *http.Request, args *struct{}, reply *struct{ ID string }) error {
	reply.ID = s.GetSupervisorID()
	return nil
}

// GetSupervisorID get the supervisor identifier from configuration file
func (s *Supervisor) GetSupervisorID() string {
	return "supervisor"
}

// GetState get the state of supervisor
func (s *Supervisor) GetState(r *http.Request, args *struct{}, reply *struct{ StateInfo StateInfo }) error {
	//statecode     statename
	//=======================
	// 2            FATAL
	// 1            RUNNING
	// 0            RESTARTING
	// -1           SHUTDOWN
	zap.L().Debug("Get state")
	reply.StateInfo.Statecode = 1
	reply.StateInfo.Statename = "RUNNING"
	return nil
}

// GetPID get the pid of supervisor
func (s *Supervisor) GetPID(r *http.Request, args *struct{}, reply *struct{ Pid int }) error {
	reply.Pid = os.Getpid()
	return nil
}

// ReadLog read the log of supervisor
func (s *Supervisor) ReadLog(r *http.Request, args *LogReadInfo, reply *struct{ Log string }) error {
	data, err := s.logger.ReadLog(int64(args.Offset), int64(args.Length))
	reply.Log = data
	return err
}

// ClearLog clear the supervisor log
func (s *Supervisor) ClearLog(r *http.Request, args *struct{}, reply *struct{ Ret bool }) error {
	err := s.logger.ClearAllLogFile()
	reply.Ret = err == nil
	return err
}

// Shutdown shutdown the supervisor
func (s *Supervisor) Shutdown(r *http.Request, args *struct{}, reply *struct{ Ret bool }) error {
	reply.Ret = true
	zap.L().Info("received rpc request to stop all processes & exit")
	s.procMgr.StopAllProcesses()
	go func() {
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
	return nil
}

// Restart restart the supervisor
func (s *Supervisor) Restart(r *http.Request, args *struct{}, reply *struct{ Ret bool }) error {
	zap.L().Info("Receive instruction to restart")
	s.restarting = true
	reply.Ret = true
	return nil
}

// IsRestarting check if supervisor is in restarting state
func (s *Supervisor) IsRestarting() bool {
	return s.restarting
}

func getProcessInfo(proc *process.Process) *types.ProcessInfo {
	return &types.ProcessInfo{
		Name:          proc.Name(),
		Group:         proc.Group(),
		Description:   proc.Description(),
		Start:         int(proc.StartTime().Unix()),
		Stop:          int(proc.StopTime().Unix()),
		Now:           int(time.Now().Unix()),
		State:         int(proc.State()),
		Statename:     proc.State().String(),
		Spawnerr:      "",
		Exitstatus:    proc.ExitStatus(),
		Logfile:       proc.StdoutLogfile(),
		StdoutLogfile: proc.StdoutLogfile(),
		StderrLogfile: proc.StderrLogfile(),
		Pid:           proc.Pid(),
	}
}

// GetAllProcessInfo get all the program informations managed by supervisor
func (s *Supervisor) GetAllProcessInfo(r *http.Request, args *struct{}, reply *struct{ AllProcessInfo []types.ProcessInfo }) error {
	reply.AllProcessInfo = make([]types.ProcessInfo, 0)
	s.procMgr.ForEachProcess(func(proc *process.Process) {
		procInfo := getProcessInfo(proc)
		reply.AllProcessInfo = append(reply.AllProcessInfo, *procInfo)
	})
	types.SortProcessInfos(reply.AllProcessInfo)
	return nil
}

// GetProcessInfo get the process information of one program
func (s *Supervisor) GetProcessInfo(r *http.Request, args *struct{ Name string }, reply *struct{ ProcInfo types.ProcessInfo }) error {
	zap.L().Info("Get process info", zap.String("name", args.Name))
	proc := s.procMgr.Find(args.Name)
	if proc == nil {
		return fmt.Errorf("no process named %s", args.Name)
	}

	reply.ProcInfo = *getProcessInfo(proc)
	return nil
}

// StartProcess start the given program
func (s *Supervisor) StartProcess(r *http.Request, args *StartProcessArgs, reply *struct{ Success bool }) error {
	procs := s.procMgr.FindMatch(args.Name)

	if len(procs) <= 0 {
		return fmt.Errorf("fail to find process %s", args.Name)
	}
	for _, proc := range procs {
		proc.Start(args.Wait)
	}
	reply.Success = true
	return nil
}

// StartAllProcesses start all the programs
func (s *Supervisor) StartAllProcesses(r *http.Request, args *struct {
	Wait bool `default:"true"`
}, reply *struct{ RPCTaskResults []RPCTaskResult }) error {
	finishedProcCh := make(chan *process.Process)

	n := s.procMgr.AsyncForEachProcess(func(proc *process.Process) {
		proc.Start(args.Wait)
	}, finishedProcCh)

	for i := 0; i < n; i++ {
		proc, ok := <-finishedProcCh
		if ok {
			processInfo := *getProcessInfo(proc)
			reply.RPCTaskResults = append(reply.RPCTaskResults, RPCTaskResult{
				Name:        processInfo.Name,
				Group:       processInfo.Group,
				Status:      faults.Success,
				Description: "OK",
			})
		}
	}
	return nil
}

// StartProcessGroup start all the processes in one group
func (s *Supervisor) StartProcessGroup(r *http.Request, args *StartProcessArgs, reply *struct{ AllProcessInfo []types.ProcessInfo }) error {
	zap.L().Info("start process group", zap.String("group", args.Name))
	finishedProcCh := make(chan *process.Process)

	n := s.procMgr.AsyncForEachProcess(func(proc *process.Process) {
		if proc.Group() == args.Name {
			proc.Start(args.Wait)
		}
	}, finishedProcCh)

	for i := 0; i < n; i++ {
		proc, ok := <-finishedProcCh
		if ok && proc.Group() == args.Name {
			reply.AllProcessInfo = append(reply.AllProcessInfo, *getProcessInfo(proc))
		}
	}

	return nil
}

// StopProcess stop given program
func (s *Supervisor) StopProcess(r *http.Request, args *StartProcessArgs, reply *struct{ Success bool }) error {
	zap.L().Info("stop process", zap.String("program", args.Name))
	procs := s.procMgr.FindMatch(args.Name)
	if len(procs) <= 0 {
		return fmt.Errorf("fail to find process %s", args.Name)
	}
	for _, proc := range procs {
		proc.Stop(args.Wait)
	}
	reply.Success = true
	return nil
}

// StopProcessGroup stop all processes in one group
func (s *Supervisor) StopProcessGroup(r *http.Request, args *StartProcessArgs, reply *struct{ AllProcessInfo []types.ProcessInfo }) error {
	zap.L().Info("stop process group", zap.String("group", args.Name))
	finishedProcCh := make(chan *process.Process)
	n := s.procMgr.AsyncForEachProcess(func(proc *process.Process) {
		if proc.Group() == args.Name {
			proc.Stop(args.Wait)
		}
	}, finishedProcCh)

	for i := 0; i < n; i++ {
		proc, ok := <-finishedProcCh
		if ok && proc.Group() == args.Name {
			reply.AllProcessInfo = append(reply.AllProcessInfo, *getProcessInfo(proc))
		}
	}
	return nil
}

// StopAllProcesses stop all programs managed by supervisor
func (s *Supervisor) StopAllProcesses(r *http.Request, args *struct {
	Wait bool `default:"true"`
}, reply *struct{ RPCTaskResults []RPCTaskResult }) error {
	finishedProcCh := make(chan *process.Process)

	n := s.procMgr.AsyncForEachProcess(func(proc *process.Process) {
		proc.Stop(args.Wait)
	}, finishedProcCh)

	for i := 0; i < n; i++ {
		proc, ok := <-finishedProcCh
		if ok {
			processInfo := *getProcessInfo(proc)
			reply.RPCTaskResults = append(reply.RPCTaskResults, RPCTaskResult{
				Name:        processInfo.Name,
				Group:       processInfo.Group,
				Status:      faults.Success,
				Description: "OK",
			})
		}
	}
	return nil
}

// SignalProcess send a signal to running program
func (s *Supervisor) SignalProcess(r *http.Request, args *types.ProcessSignal, reply *struct{ Success bool }) error {
	procs := s.procMgr.FindMatch(args.Name)
	if len(procs) <= 0 {
		reply.Success = false
		return fmt.Errorf("No process named %s", args.Name)
	}
	sig, err := signals.ToSignal(args.Signal)
	if err == nil {
		for _, proc := range procs {
			proc.Signal(sig, false)
		}
	}
	reply.Success = true
	return nil
}

// SignalProcessGroup send signal to all processes in one group
func (s *Supervisor) SignalProcessGroup(r *http.Request, args *types.ProcessSignal, reply *struct{ AllProcessInfo []types.ProcessInfo }) error {
	s.procMgr.ForEachProcess(func(proc *process.Process) {
		if proc.Group() == args.Name {
			sig, err := signals.ToSignal(args.Signal)
			if err == nil {
				proc.Signal(sig, false)
			}
		}
	})

	s.procMgr.ForEachProcess(func(proc *process.Process) {
		if proc.Group() == args.Name {
			reply.AllProcessInfo = append(reply.AllProcessInfo, *getProcessInfo(proc))
		}
	})
	return nil
}

// SignalAllProcesses send signal to all the processes in the supervisor
func (s *Supervisor) SignalAllProcesses(r *http.Request, args *types.ProcessSignal, reply *struct{ AllProcessInfo []types.ProcessInfo }) error {
	s.procMgr.ForEachProcess(func(proc *process.Process) {
		sig, err := signals.ToSignal(args.Signal)
		if err == nil {
			proc.Signal(sig, false)
		}
	})
	s.procMgr.ForEachProcess(func(proc *process.Process) {
		reply.AllProcessInfo = append(reply.AllProcessInfo, *getProcessInfo(proc))
	})
	return nil
}

// SendProcessStdin send data to program through stdin
func (s *Supervisor) SendProcessStdin(r *http.Request, args *ProcessStdin, reply *struct{ Success bool }) error {
	proc := s.procMgr.Find(args.Name)
	if proc == nil {
		zap.L().Error("program does not exist", zap.String("program", args.Name))
		return fmt.Errorf("NOT_RUNNING")
	}
	if proc.State() != process.Running {
		zap.L().Error("program does not run", zap.String("program", args.Name))
		return fmt.Errorf("NOT_RUNNING")
	}
	err := proc.SendProcessStdin(args.Chars)
	if err == nil {
		reply.Success = true
	} else {
		reply.Success = false
	}
	return err
}

// Reload reload the supervisor configuration
//return err, addedGroup, changedGroup, removedGroup
//
func (s *Supervisor) Reload() (addedGroup, changedGroup, removedGroup []string, err error) {
	// get the previous loaded programs
	prevPrograms := s.config.ProgramNames()
	prevProgGroup := s.config.ProgramGroup.Clone()

	loadedPrograms, err := s.config.Load()

	if err == nil {
		s.createPrograms(prevPrograms)
		s.startHTTPServer()
		s.startAutoStartPrograms()
	}
	removedPrograms := util.Sub(prevPrograms, loadedPrograms)
	for _, removedProg := range removedPrograms {
		zap.L().Info("the program is removed and will be stopped", zap.String("program", removedProg))
		s.config.RemoveProgram(removedProg)
		proc := s.procMgr.Remove(removedProg)
		if proc != nil {
			proc.Stop(false)
		}

	}
	addedGroup, changedGroup, removedGroup = s.config.ProgramGroup.Sub(prevProgGroup)
	return addedGroup, changedGroup, removedGroup, err
}

// WaitForExit wait the superisor to exit
func (s *Supervisor) WaitForExit() {
	for {
		if s.IsRestarting() {
			s.procMgr.StopAllProcesses()
			break
		}
		time.Sleep(10 * time.Second)
	}
}

func (s *Supervisor) createPrograms(prevPrograms []string) {
	programs := s.config.ProgramNames()
	for _, program := range s.config.Programs() {
		s.procMgr.CreateProcess(s.GetSupervisorID(), program)
	}
	removedPrograms := util.Sub(prevPrograms, programs)
	for _, p := range removedPrograms {
		s.procMgr.Remove(p)
	}
}

func (s *Supervisor) startAutoStartPrograms() {
	s.procMgr.StartAutoStartPrograms()
}

func (s *Supervisor) startHTTPServer() {
	s.xmlRPC.Stop()

	if cfg := s.config.InetHTTPServer; cfg != nil {
		addr := cfg.Port
		if addr != "" {
			cond := sync.NewCond(&sync.Mutex{})
			cond.L.Lock()
			defer cond.L.Unlock()
			go s.xmlRPC.StartInetHTTPServer(cfg.Username, cfg.Password, addr, s, func() { cond.Signal() })
			cond.Wait()
		}
	}

	if cfg := s.config.UnixHTTPServer; cfg != nil {
		cond := sync.NewCond(&sync.Mutex{})
		cond.L.Lock()
		defer cond.L.Unlock()
		go s.xmlRPC.StartUnixHTTPServer(cfg.Username,
			cfg.Password,
			cfg.File,
			s,
			func() {
				cond.Signal()
			})
		cond.Wait()
	}
}

// ReloadConfig reload the supervisor configuration file
func (s *Supervisor) ReloadConfig(r *http.Request, args *struct{}, reply *types.ReloadConfigResult) error {
	zap.L().Info("start to reload config")
	addedGroup, changedGroup, removedGroup, err := s.Reload()
	if len(addedGroup) > 0 {
		zap.L().Info("added groups", zap.Strings("groups", addedGroup))
	}

	if len(changedGroup) > 0 {
		zap.L().Info("changed groups", zap.Strings("groups", changedGroup))
	}

	if len(removedGroup) > 0 {
		zap.L().Info("removed groups", zap.Strings("groups", removedGroup))
	}
	reply.AddedGroup = addedGroup
	reply.ChangedGroup = changedGroup
	reply.RemovedGroup = removedGroup
	return err
}

// AddProcessGroup add a process group to the supervisor
func (s *Supervisor) AddProcessGroup(r *http.Request, args *struct{ Name string }, reply *struct{ Success bool }) error {
	reply.Success = false
	return nil
}

// RemoveProcessGroup remove a process group from the supervisor
func (s *Supervisor) RemoveProcessGroup(r *http.Request, args *struct{ Name string }, reply *struct{ Success bool }) error {
	reply.Success = false
	return nil
}

// ReadProcessStdoutLog read the stdout log of a given program
func (s *Supervisor) ReadProcessStdoutLog(r *http.Request, args *ProcessLogReadInfo, reply *struct{ LogData string }) error {
	proc := s.procMgr.Find(args.Name)
	if proc == nil {
		return fmt.Errorf("No such process %s", args.Name)
	}
	var err error
	reply.LogData, err = proc.StdoutLog.ReadLog(int64(args.Offset), int64(args.Length))
	return err
}

// ReadProcessStderrLog read the stderr log of a given program
func (s *Supervisor) ReadProcessStderrLog(r *http.Request, args *ProcessLogReadInfo, reply *struct{ LogData string }) error {
	proc := s.procMgr.Find(args.Name)
	if proc == nil {
		return fmt.Errorf("No such process %s", args.Name)
	}
	var err error
	reply.LogData, err = proc.StderrLog.ReadLog(int64(args.Offset), int64(args.Length))
	return err
}

// TailProcessStdoutLog tail the stdout of a program
func (s *Supervisor) TailProcessStdoutLog(r *http.Request, args *ProcessLogReadInfo, reply *ProcessTailLog) error {
	proc := s.procMgr.Find(args.Name)
	if proc == nil {
		return fmt.Errorf("No such process %s", args.Name)
	}
	var err error
	reply.LogData, reply.Offset, reply.Overflow, err = proc.StdoutLog.ReadTailLog(int64(args.Offset), int64(args.Length))
	return err
}

// TailProcessStderrLog tail the stderr of a program
func (s *Supervisor) TailProcessStderrLog(r *http.Request, args *ProcessLogReadInfo, reply *ProcessTailLog) error {
	proc := s.procMgr.Find(args.Name)
	if proc == nil {
		return fmt.Errorf("No such process %s", args.Name)
	}
	var err error
	reply.LogData, reply.Offset, reply.Overflow, err = proc.StderrLog.ReadTailLog(int64(args.Offset), int64(args.Length))
	return err
}

// ClearProcessLogs clear the log of a given program
func (s *Supervisor) ClearProcessLogs(r *http.Request, args *struct{ Name string }, reply *struct{ Success bool }) error {
	proc := s.procMgr.Find(args.Name)
	if proc == nil {
		return fmt.Errorf("No such process %s", args.Name)
	}
	err1 := proc.StdoutLog.ClearAllLogFile()
	err2 := proc.StderrLog.ClearAllLogFile()
	reply.Success = err1 == nil && err2 == nil
	if err1 != nil {
		return err1
	}
	return err2
}

// ClearAllProcessLogs clear the logs of all programs
func (s *Supervisor) ClearAllProcessLogs(r *http.Request, args *struct{}, reply *struct{ RPCTaskResults []RPCTaskResult }) error {
	s.procMgr.ForEachProcess(func(proc *process.Process) {
		proc.StdoutLog.ClearAllLogFile()
		proc.StderrLog.ClearAllLogFile()
		procInfo := getProcessInfo(proc)
		reply.RPCTaskResults = append(reply.RPCTaskResults, RPCTaskResult{
			Name:        procInfo.Name,
			Group:       procInfo.Group,
			Status:      faults.Success,
			Description: "OK",
		})
	})

	return nil
}

// GetManager get the Manager object created by superisor
func (s *Supervisor) GetManager() *process.Manager {
	return s.procMgr
}
