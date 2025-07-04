// Copyright 2021 TiKV Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// NOTE: The code in this file is based on code from the
// TiDB project, licensed under the Apache License v 2.0
//
// https://github.com/pingcap/tidb/tree/cc5e161ac06827589c4966674597c137cc9e809c/store/tikv/error/error.go
//

// Copyright 2016 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package error

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/pingcap/kvproto/pkg/pdpb"
	"github.com/pingcap/log"
	"github.com/pkg/errors"
	"github.com/tikv/client-go/v2/internal/logutil"
	"github.com/tikv/client-go/v2/metrics"
	"github.com/tikv/client-go/v2/util"
	"github.com/tikv/client-go/v2/util/redact"
	"go.uber.org/zap"
)

var (
	// ErrBodyMissing response body is missing error
	ErrBodyMissing = errors.New("response body is missing")
	// ErrTiDBShuttingDown is returned when TiDB is closing and send request to tikv fail, do not retry.
	ErrTiDBShuttingDown = errors.New("tidb server shutting down")
	// ErrNotExist means the related data not exist.
	ErrNotExist = errors.New("not exist")
	// ErrCannotSetNilValue is the error when sets an empty value.
	ErrCannotSetNilValue = errors.New("can not set nil value")
	// ErrInvalidTxn is the error when commits or rollbacks in an invalid transaction.
	ErrInvalidTxn = errors.New("invalid transaction")
	// ErrTiKVServerTimeout is the error when tikv server is timeout.
	ErrTiKVServerTimeout = errors.New("tikv server timeout")
	// ErrTiFlashServerTimeout is the error when tiflash server is timeout.
	ErrTiFlashServerTimeout = errors.New("tiflash server timeout")
	// ErrQueryInterrupted is the error when the query is interrupted.
	// This is deprecated. Keep it only to pass CI :-(. We can remove this later.
	ErrQueryInterrupted = errors.New("query interrupted")
	// ErrTiKVStaleCommand is the error that the command is stale in tikv.
	ErrTiKVStaleCommand = errors.New("tikv stale command")
	// ErrTiKVMaxTimestampNotSynced is the error that tikv's max timestamp is not synced.
	ErrTiKVMaxTimestampNotSynced = errors.New("tikv max timestamp not synced")
	// ErrLockAcquireFailAndNoWaitSet is the error that acquire the lock failed while no wait is setted.
	ErrLockAcquireFailAndNoWaitSet = errors.New("lock acquired failed and no wait is set")
	// ErrResolveLockTimeout is the error that resolve lock timeout.
	ErrResolveLockTimeout = errors.New("resolve lock timeout")
	// ErrLockWaitTimeout is the error that wait for the lock is timeout.
	ErrLockWaitTimeout = errors.New("lock wait timeout")
	// ErrTiKVServerBusy is the error when tikv server is busy.
	ErrTiKVServerBusy = errors.New("tikv server busy")
	// ErrTiFlashServerBusy is the error that tiflash server is busy.
	ErrTiFlashServerBusy = errors.New("tiflash server busy")
	// ErrRegionUnavailable is the error when region is not available.
	ErrRegionUnavailable = errors.New("region unavailable")
	// ErrRegionDataNotReady is the error when region's data is not ready when querying it with safe_ts
	ErrRegionDataNotReady = errors.New("region data not ready")
	// ErrRegionNotInitialized is error when region is not initialized
	ErrRegionNotInitialized = errors.New("region not Initialized")
	// ErrTiKVDiskFull is the error when tikv server disk usage is full.
	ErrTiKVDiskFull = errors.New("tikv disk full")
	// ErrRegionRecoveryInProgress is the error when region is recovering.
	ErrRegionRecoveryInProgress = errors.New("region is being online unsafe recovered")
	// ErrRegionFlashbackInProgress is the error when a region in the flashback progress receive any other request.
	ErrRegionFlashbackInProgress = errors.New("region is in the flashback progress")
	// ErrRegionFlashbackNotPrepared is the error when a region is not prepared for the flashback first.
	ErrRegionFlashbackNotPrepared = errors.New("region is not prepared for the flashback")
	// ErrIsWitness is the error when a request is send to a witness.
	ErrIsWitness = errors.New("peer is witness")
	// ErrUnknown is the unknow error.
	ErrUnknown = errors.New("unknown")
	// ErrResultUndetermined is the error when execution result is unknown.
	ErrResultUndetermined = errors.New("execution result undetermined")
)

type ErrQueryInterruptedWithSignal struct {
	Signal uint32
}

func (e ErrQueryInterruptedWithSignal) Error() string {
	return fmt.Sprintf("query interrupted by signal %d", e.Signal)
}

// MismatchClusterID represents the message that the cluster ID of the PD client does not match the PD.
const MismatchClusterID = "mismatch cluster id"

// IsErrNotFound checks if err is a kind of NotFound error.
func IsErrNotFound(err error) bool {
	return errors.Is(err, ErrNotExist)
}

// ErrDeadlock wraps *kvrpcpb.Deadlock to implement the error interface.
// It also marks if the deadlock is retryable.
type ErrDeadlock struct {
	*kvrpcpb.Deadlock
	IsRetryable bool
}

func (d *ErrDeadlock) Error() string {
	return d.Deadlock.String()
}

// PDError wraps *pdpb.Error to implement the error interface.
type PDError struct {
	Err *pdpb.Error
}

func (d *PDError) Error() string {
	return d.Err.String()
}

// ErrKeyExist wraps *pdpb.AlreadyExist to implement the error interface.
type ErrKeyExist struct {
	*kvrpcpb.AlreadyExist
	// Value stores the value that is being written when the conflict is detected.
	// It was supposed to be an Insert mutation so the value should exist.
	// The value is not in the kv protocol,
	// it is introduced for error message handling in pipeliend-DML.
	Value []byte
}

func (k *ErrKeyExist) Error() string {
	return k.AlreadyExist.String()
}

// IsErrKeyExist returns true if it is ErrKeyExist.
func IsErrKeyExist(err error) bool {
	var e *ErrKeyExist
	return errors.As(err, &e)
}

// ErrWriteConflict wraps *kvrpcpb.ErrWriteConflict to implement the error interface.
type ErrWriteConflict struct {
	*kvrpcpb.WriteConflict
}

func (k *ErrWriteConflict) Error() string {
	return fmt.Sprintf("write conflict { %s }", k.WriteConflict.String())
}

// IsErrWriteConflict returns true if it is ErrWriteConflict.
func IsErrWriteConflict(err error) bool {
	var e *ErrWriteConflict
	return errors.As(err, &e)
}

// NewErrWriteConflict generates an ErrWriteConflict with conflict and increase the TiKVTxnWriteConflictCounter metrics.
func NewErrWriteConflict(conflict *kvrpcpb.WriteConflict) *ErrWriteConflict {
	metrics.TiKVTxnWriteConflictCounter.Inc()
	return &ErrWriteConflict{WriteConflict: conflict}
}

// NewErrWriteConflictWithArgs generates an ErrWriteConflict with args.
func NewErrWriteConflictWithArgs(startTs, conflictTs, conflictCommitTs uint64, key []byte, reason kvrpcpb.WriteConflict_Reason) *ErrWriteConflict {
	return NewErrWriteConflict(&kvrpcpb.WriteConflict{
		StartTs:          startTs,
		ConflictTs:       conflictTs,
		Key:              key,
		ConflictCommitTs: conflictCommitTs,
		Reason:           reason,
	})
}

// ErrWriteConflictInLatch is the error when the commit meets an write conflict error when local latch is enabled.
type ErrWriteConflictInLatch struct {
	StartTS uint64
}

func (e *ErrWriteConflictInLatch) Error() string {
	return fmt.Sprintf("write conflict in latch,startTS: %v", e.StartTS)
}

// ErrRetryable wraps *kvrpcpb.Retryable to implement the error interface.
type ErrRetryable struct {
	Retryable string
}

func (k *ErrRetryable) Error() string {
	return k.Retryable
}

// ErrTxnTooLarge is the error when transaction is too large, lock time reached the maximum value.
type ErrTxnTooLarge struct {
	Size int
}

func (e *ErrTxnTooLarge) Error() string {
	return fmt.Sprintf("txn too large, size: %v.", e.Size)
}

type ErrKeyTooLarge struct {
	KeySize int
}

func (e *ErrKeyTooLarge) Error() string {
	return fmt.Sprintf("key size too large, size: %v.", e.KeySize)
}

// ErrEntryTooLarge is the error when a key value entry is too large.
type ErrEntryTooLarge struct {
	Limit uint64
	Size  uint64
}

func (e *ErrEntryTooLarge) Error() string {
	return fmt.Sprintf("entry size too large, size: %v,limit: %v.", e.Size, e.Limit)
}

// ErrPDServerTimeout is the error when pd server is timeout.
type ErrPDServerTimeout struct {
	msg string
}

// NewErrPDServerTimeout creates an ErrPDServerTimeout.
func NewErrPDServerTimeout(msg string) error {
	return &ErrPDServerTimeout{msg}
}

func (e *ErrPDServerTimeout) Error() string {
	return e.msg
}

// ErrGCTooEarly is the error that GC life time is shorter than transaction duration
// For compatibility concerns of exported interfaces, keep the old error type.
// Deprecated: use ErrTxnAbortedByGC instead.
type ErrGCTooEarly struct {
	TxnStartTS  time.Time
	GCSafePoint time.Time
}

func (e *ErrGCTooEarly) Error() string {
	return fmt.Sprintf("GC life time is shorter than transaction duration, transaction starts at %v, GC safe point is %v", e.TxnStartTS, e.GCSafePoint)
}

// ErrTxnAbortedByGC is the error that GC progress has advanced to a state in which the txn is no longer safe to
// continue.
type ErrTxnAbortedByGC struct {
	TxnStartTS       uint64
	TxnStartTSTime   time.Time
	TxnSafePoint     uint64
	TxnSafePointTime time.Time
}

func (e *ErrTxnAbortedByGC) Error() string {
	// In most cases, the error is caused by the transaction runs too long, instead of improper GC life time
	// configuration. This means the description of this error is not accurate.
	// However, as this error message is already widely acknowledged and might have become part of our diagnosing
	// process, we keep the error message unchanged.
	return fmt.Sprintf("GC life time is shorter than transaction duration, transaction start ts is %v (%v), txn safe point is %v (%v)", e.TxnStartTS, e.TxnStartTSTime, e.TxnSafePoint, e.TxnSafePointTime)
}

// ErrTokenLimit is the error that token is up to the limit.
type ErrTokenLimit struct {
	StoreID uint64
}

func (e *ErrTokenLimit) Error() string {
	return fmt.Sprintf("Store token is up to the limit, store id = %d.", e.StoreID)
}

// ErrAssertionFailed is the error that assertion on data failed.
type ErrAssertionFailed struct {
	*kvrpcpb.AssertionFailed
}

// ErrLockOnlyIfExistsNoReturnValue is used when the flag `LockOnlyIfExists` of `LockCtx` is set, but `ReturnValues` is not.
type ErrLockOnlyIfExistsNoReturnValue struct {
	StartTS     uint64
	ForUpdateTs uint64
	LockKey     []byte
}

// ErrLockOnlyIfExistsNoPrimaryKey is used when the flag `LockOnlyIfExists` of `LockCtx` is set, but primary key of current transaction is not.
type ErrLockOnlyIfExistsNoPrimaryKey struct {
	StartTS     uint64
	ForUpdateTs uint64
	LockKey     []byte
}

func (e *ErrAssertionFailed) Error() string {
	return fmt.Sprintf("assertion failed { %s }", e.AssertionFailed.String())
}

func (e *ErrLockOnlyIfExistsNoReturnValue) Error() string {
	return fmt.Sprintf("LockOnlyIfExists is set for Lock Context, but ReturnValues is not set, "+
		"StartTs is {%d}, ForUpdateTs is {%d}, one of lock keys is {%v}.",
		e.StartTS, e.ForUpdateTs, redact.Key(e.LockKey))
}

func (e *ErrLockOnlyIfExistsNoPrimaryKey) Error() string {
	return fmt.Sprintf("LockOnlyIfExists is set for Lock Context, but primary key of current transaction is not set, "+
		"StartTs is {%d}, ForUpdateTs is {%d}, one of lock keys is {%s}",
		e.StartTS, e.ForUpdateTs, redact.Key(e.LockKey))
}

// ExtractKeyErr extracts a KeyError.
func ExtractKeyErr(keyErr *kvrpcpb.KeyError) error {
	if val, err := util.EvalFailpoint("mockRetryableErrorResp"); err == nil {
		if val.(bool) {
			keyErr.Conflict = nil
			keyErr.Retryable = "mock retryable error"
		}
	}

	redact.RedactKeyErrIfNecessary(keyErr)

	if keyErr.Conflict != nil {
		return errors.WithStack(NewErrWriteConflict(keyErr.GetConflict()))
	}

	if keyErr.Retryable != "" {
		return errors.WithStack(&ErrRetryable{Retryable: keyErr.Retryable})
	}

	if keyErr.AssertionFailed != nil {
		return &ErrAssertionFailed{AssertionFailed: keyErr.AssertionFailed}
	}

	if keyErr.Abort != "" {
		err := errors.Errorf("tikv aborts txn: %s", keyErr.GetAbort())
		logutil.BgLogger().Warn("2PC failed", zap.Error(err))
		return err
	}
	if keyErr.CommitTsTooLarge != nil {
		err := errors.Errorf("commit TS %v is too large", keyErr.CommitTsTooLarge.CommitTs)
		logutil.BgLogger().Warn("2PC failed", zap.Error(err))
		return err
	}
	if keyErr.TxnNotFound != nil {
		err := errors.Errorf("txn %d not found", keyErr.TxnNotFound.StartTs)
		return err
	}
	return errors.Errorf("unexpected KeyError: %s", keyErr.String())
}

// IsErrorUndetermined checks if the error is undetermined error.
func IsErrorUndetermined(err error) bool {
	return errors.Is(err, ErrResultUndetermined)
}

// Log logs the error if it is not nil.
func Log(err error) {
	if err != nil {
		log.Error("encountered error", zap.Error(err), zap.Stack("stack"))
	}
}

// ExtractDebugInfoStrFromKeyErr extracts the debug info from key error
func ExtractDebugInfoStrFromKeyErr(keyErr *kvrpcpb.KeyError) string {
	if keyErr.DebugInfo == nil {
		return ""
	}

	debugInfoToMarshal := keyErr.DebugInfo
	if redact.NeedRedact() {
		redactMarker := []byte{'?'}
		debugInfoToMarshal = proto.Clone(debugInfoToMarshal).(*kvrpcpb.DebugInfo)
		for _, mvccInfo := range debugInfoToMarshal.MvccInfo {
			mvccInfo.Key = redactMarker
			if mvcc := mvccInfo.Mvcc; mvcc != nil {
				if lock := mvcc.Lock; lock != nil {
					lock.Primary = redactMarker
					lock.ShortValue = redactMarker
					for i := range lock.Secondaries {
						lock.Secondaries[i] = redactMarker
					}
				}

				for _, write := range mvcc.Writes {
					if write != nil {
						write.ShortValue = redactMarker
					}
				}

				for _, value := range mvcc.Values {
					if value != nil {
						value.Value = redactMarker
					}
				}
			}
		}
	}

	debugStr, err := json.Marshal(debugInfoToMarshal)
	if err != nil {
		log.Error("encountered error when extracting debug info for keyError", zap.Error(err), zap.Stack("stack"))
		return ""
	}
	return string(debugStr)
}
