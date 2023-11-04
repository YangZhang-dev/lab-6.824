package raft

type RequestVoteReply struct {
	FollowerId  int
	VoteGranted bool
	Term        int
}

type RequestEntityArgs struct {
	LeaderId     int
	Term         int
	LeaderCommit int
	PrevLogTerm  int
	PrevLogIndex int
	Entries      []Log
}

type RequestEntityReply struct {
	FollowerId int
	Term       int
	Conflict   bool
	XIndex     int
	Success    bool
}

type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}
type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Data              []byte
}
type InstallSnapshotReply struct {
	Term int
}

func (rf *Raft) RequestEntity(args *RequestEntityArgs, reply *RequestEntityReply) {

	//rf.xlog("current log is %+v", rf.logs.LogList)
	term := args.Term
	prevLogIndex := args.PrevLogIndex
	prevLogTerm := args.PrevLogTerm
	leaderCommit := args.LeaderCommit
	entries := args.Entries
	reply.Success = false
	reply.Conflict = false
	rf.mu.Lock()
	defer func() {
		rf.persist()
		rf.mu.Unlock()
	}()

	if len(args.Entries) >= 1 {
		rf.xlog("get EV request,leader %d, args %+v,first log is %+v, last log is %+v", args.LeaderId, args, args.Entries[:1], args.Entries[len(args.Entries)-1:])
	} else {
		rf.xlog("get EV request,leader %d, args %+v", args.LeaderId, args)
	}
	reply.FollowerId = rf.me
	reply.Term = rf.currentTerm
	if term > rf.currentTerm {
		rf.startNewTerm(term)
	}
	if term < rf.currentTerm {
		return
	}

	rf.RestartVoteEndTime()
	rf.setMembership(FOLLOWER)

	l := rf.logs.getLogByIndex(prevLogIndex)
	if l.Term != prevLogTerm {
		rf.xlog("get conflict")
		reply.Conflict = true
		xIndex := prevLogIndex
		if prevLogIndex <= rf.logs.lastIncludedIndex {
			reply.XIndex = xIndex
			rf.xlog("from index %d start snapshot", prevLogIndex)
			return
		}

		// A
		lastLog := rf.logs.getLastLog()
		if lastLog.Index < prevLogIndex {
			xIndex = lastLog.Index + 1
			reply.XIndex = xIndex
			rf.xlog("SEC A, xindex is %+v", xIndex)
			return
		}
		// C
		rf.xlog("SEC C")
		tailTerm := l.Term
		rf.xlog("get conflict term tail index %d", xIndex)
		for i := xIndex - 1; i > rf.commitIndex; i-- {
			if i <= rf.logs.lastIncludedIndex {
				rf.xlog("from index %d start snapshot", i)
				break
			}
			log := rf.logs.getLogByIndex(i)
			if tailTerm != log.Term {
				rf.xlog("not match ,tailTerm: %d,logTerm %d", tailTerm, log.Term)
				break
			}
			xIndex = i
		}
		rf.xlog("get conflict log head index %d", xIndex)
		reply.XIndex = xIndex
		rf.xlog("term do not match,return false，xindex:%v", xIndex)
		return
	}

	// B D
	rf.logs.removeTailLogs(prevLogIndex)
	rf.logs.storeLog(entries...)
	rf.xlog("after store, current log is %+v", rf.logs.LogList)
	if rf.commitIndex < leaderCommit {
		if leaderCommit > rf.logs.getLastLogIndex() {
			leaderCommit = rf.logs.getLastLogIndex()
		}

		for rf.commitIndex < leaderCommit {
			rf.commitIndex++
			msg := ApplyMsg{
				CommandValid: true,
				Command:      rf.logs.getLogByIndex(rf.commitIndex).Content,
				CommandIndex: rf.commitIndex,
			}
			rf.sendCh <- msg
			rf.xlog("从leader%v,同步完成,msg:%+v", args.LeaderId, msg)
		}
		rf.commitIndex = leaderCommit
		// TODO
		rf.lastApplied = rf.commitIndex
		rf.xlog("从leader%v,同步完成,log is %+v", args.LeaderId, rf.logs.LogList)
	}
	reply.Success = true
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	term := args.Term
	candidateId := args.CandidateId
	lastLogIndex := args.LastLogIndex
	lastLogTerm := args.LastLogTerm

	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.currentTerm
	reply.FollowerId = rf.me
	reply.VoteGranted = false

	if term < rf.currentTerm {
		return
	}
	if term > rf.currentTerm {
		rf.startNewTerm(term)
	}

	if rf.voteFor != VOTE_NO && rf.voteFor != candidateId {
		return
	}
	lastLog := rf.logs.getLastLog()
	if lastLogTerm < lastLog.Term {
		return
	}
	if lastLogTerm == lastLog.Term && lastLogIndex < lastLog.Index {
		return
	}

	reply.VoteGranted = true
	rf.RestartVoteEndTime()
	rf.voteFor = candidateId
	rf.setMembership(FOLLOWER)
	rf.persist()
}

func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.xlog("get a snapshot request,last index %d,last term %d", args.LastIncludedIndex, args.LastIncludedTerm)
	data := args.Data
	term := args.Term
	lastIncludedIndex := args.LastIncludedIndex
	lastIncludedTerm := args.LastIncludedTerm
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.currentTerm

	if term < rf.currentTerm {
		return
	}
	if term > rf.currentTerm {
		rf.startNewTerm(term)
	}
	if lastIncludedIndex <= rf.logs.lastIncludedIndex {
		return
	}
	rf.logs.tLastIncludedIndex = lastIncludedIndex
	rf.logs.tLastIncludedTerm = lastIncludedTerm
	applyMsg := ApplyMsg{
		SnapshotValid: true,
		Snapshot:      data,
		SnapshotTerm:  lastIncludedTerm,
		SnapshotIndex: lastIncludedIndex,
	}
	rf.sendCh <- applyMsg
	rf.xlog("已完成snapshot request，wait install,current log is %+v", rf.logs.LogList)
}

func (rf *Raft) sendRequestEntity(server int, args *RequestEntityArgs, reply *RequestEntityReply) bool {
	ok := rf.peers[server].Call("Raft.RequestEntity", args, reply)
	return ok
}
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}
func (rf *Raft) sendRequestSnapshot(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	ok := rf.peers[server].Call("Raft.InstallSnapshot", args, reply)
	return ok
}
