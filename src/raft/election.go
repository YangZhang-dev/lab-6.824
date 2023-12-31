package raft

import (
	"time"
)

// true 超时
func checkTime(lastTime, timeout int64) (bool, time.Duration) {
	currentTime := time.Now()
	voteEndTime := time.UnixMilli(lastTime)
	voteTimeout := time.Duration(timeout) * time.Millisecond

	elapsed := currentTime.Sub(voteEndTime)
	duration := voteTimeout - time.Duration(TIMEOUT_OFFSET)*time.Millisecond - elapsed
	if duration > 0 {
		return false, duration
	}
	return true, duration

}

func (rf *Raft) ticker() {
	ticker := time.NewTicker(time.Duration(rf.voteTimeout) * time.Millisecond)
	for rf.killed() == false {
		rf.mu.Lock()
		state := rf.state
		voteTimeout := rf.voteTimeout
		rf.mu.Unlock()
		if state == LEADER {
			rf.voteCond.L.Lock()
			for {
				rf.mu.Lock()
				if rf.state != LEADER {
					rf.mu.Unlock()
					break
				}
				rf.mu.Unlock()
				rf.voteCond.Wait()
			}
			rf.voteCond.L.Unlock()
			ticker.Reset(time.Duration(voteTimeout) * time.Millisecond)
		}

		select {
		case <-ticker.C:
			rf.mu.Lock()
			if rf.state == LEADER {
				rf.mu.Unlock()
				break
			}
			timeout, duration := checkTime(rf.voteEndTime, rf.voteTimeout)
			rf.mu.Unlock()
			if timeout {
				rf.election()
			} else {
				ticker.Reset(duration)
			}
		}
	}

}
func (rf *Raft) election() {
	rf.mu.Lock()
	rf.xlog("start a election")
	rf.currentTerm++
	rf.voteFor = rf.me
	rf.persist()
	if rf.state == CANDIDATE {
		rf.voteTimeout = int64(rf.rand.Intn(VOTE_TIMEOUT_RANGE) + BASE_VOTE_TIMEOUT)
	}
	rf.restartVoteEndTime()
	rf.setState(CANDIDATE)

	rf.mu.Unlock()
	go rf.electionHandler()
}
func (rf *Raft) electionHandler() {
	counter := 1
	rf.mu.Lock()
	lastLog := rf.logs.getLastLog()
	args := RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: lastLog.Index,
		LastLogTerm:  lastLog.Term,
	}
	rf.mu.Unlock()
	for i := range rf.peers {
		if i == rf.me {
			continue
		}
		go func(serverId int, counter *int) {
			reply := RequestVoteReply{}
			rf.mu.Lock()
			if rf.state != CANDIDATE {
				rf.mu.Unlock()
				return
			}
			rf.mu.Unlock()
			ok := rf.sendRequestVote(serverId, &args, &reply)
			if !ok {
				return
			}
			rf.mu.Lock()
			defer rf.mu.Unlock()
			if reply.Term > rf.currentTerm {
				rf.startNewTerm(reply.Term)
				return
			}
			if args.Term != rf.currentTerm || rf.state != CANDIDATE {
				return
			}
			if reply.VoteGranted {
				rf.xlog("get server %d vote", reply.FollowerId)
				*counter++
			}
			if *counter >= rf.majority {
				rf.setState(LEADER)
			}
		}(i, &counter)
	}
}

// must lock
func (rf *Raft) startNewTerm(term int) {
	rf.currentTerm = term
	rf.setState(FOLLOWER)
	rf.voteFor = VOTE_NO
	rf.persist()
}
