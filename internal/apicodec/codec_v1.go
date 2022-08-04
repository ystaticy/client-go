package apicodec

import (
	"github.com/pingcap/kvproto/pkg/errorpb"
	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/tikv/client-go/v2/tikvrpc"
)

type codecV1 struct {
	memCodec memCodec
}

// NewCodecV1 returns a codec that can be used to encode/decode
// keys and requests to and from APIv1 format.
func NewCodecV1(mode Mode) *codecV1 {
	switch mode {
	case ModeRaw:
		return &codecV1{memCodec: &defaultMemCodec{}}
	case ModeTxn:
		return &codecV1{memCodec: &memComparableCodec{}}
	}
	panic("unknown mode")
}

func (c *codecV1) GetAPIVersion() kvrpcpb.APIVersion {
	return kvrpcpb.APIVersion_V1
}

func (c *codecV1) EncodeRequest(req *tikvrpc.Request) (*tikvrpc.Request, error) {
	return req, nil
}

func (c *codecV1) DecodeResponse(req *tikvrpc.Request, resp *tikvrpc.Response) (*tikvrpc.Response, error) {
	regionError, err := resp.GetRegionError()
	if err != nil {
		return nil, err
	}
	decodeRegionError, err := c.decodeRegionError(regionError)
	if err != nil {
		return nil, err
	}
	switch req.Type {
	case tikvrpc.CmdGet:
		r := resp.Resp.(*kvrpcpb.GetResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdScan:
		r := resp.Resp.(*kvrpcpb.ScanResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdPrewrite:
		r := resp.Resp.(*kvrpcpb.PrewriteResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdCommit:
		r := resp.Resp.(*kvrpcpb.CommitResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdCleanup:
		r := resp.Resp.(*kvrpcpb.CleanupResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdBatchGet:
		r := resp.Resp.(*kvrpcpb.BatchGetResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdBatchRollback:
		r := resp.Resp.(*kvrpcpb.BatchRollbackResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdScanLock:
		r := resp.Resp.(*kvrpcpb.ScanLockResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdResolveLock:
		r := resp.Resp.(*kvrpcpb.ResolveLockResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdGC:
		r := resp.Resp.(*kvrpcpb.GCResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdDeleteRange:
		r := resp.Resp.(*kvrpcpb.DeleteRangeResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdPessimisticLock:
		r := resp.Resp.(*kvrpcpb.PessimisticLockResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdPessimisticRollback:
		r := resp.Resp.(*kvrpcpb.PessimisticRollbackResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdTxnHeartBeat:
		r := resp.Resp.(*kvrpcpb.TxnHeartBeatResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdCheckTxnStatus:
		r := resp.Resp.(*kvrpcpb.CheckTxnStatusResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdCheckSecondaryLocks:
		r := resp.Resp.(*kvrpcpb.CheckSecondaryLocksResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawGet:
		r := resp.Resp.(*kvrpcpb.RawGetResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawBatchGet:
		r := resp.Resp.(*kvrpcpb.RawBatchGetResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawPut:
		r := resp.Resp.(*kvrpcpb.RawPutResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawBatchPut:
		r := resp.Resp.(*kvrpcpb.RawBatchPutResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawDelete:
		r := resp.Resp.(*kvrpcpb.RawDeleteResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawBatchDelete:
		r := resp.Resp.(*kvrpcpb.RawBatchDeleteResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawDeleteRange:
		r := resp.Resp.(*kvrpcpb.RawDeleteRangeResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawScan:
		r := resp.Resp.(*kvrpcpb.RawScanResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdGetKeyTTL:
		r := resp.Resp.(*kvrpcpb.RawGetKeyTTLResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawCompareAndSwap:
		r := resp.Resp.(*kvrpcpb.RawCASResponse)
		r.RegionError = decodeRegionError
	case tikvrpc.CmdRawChecksum:
		r := resp.Resp.(*kvrpcpb.RawChecksumResponse)
		r.RegionError = decodeRegionError
	}
	return resp, nil
}

func (c *codecV1) EncodeRegionKey(key []byte) []byte {
	// In the context of region key, nil or empty slice has the special meaning of no bound,
	// so we skip encoding if given key is empty.
	if len(key) == 0 {
		return key
	}
	return c.memCodec.encodeKey(key)
}

func (c *codecV1) DecodeRegionKey(encodedKey []byte) ([]byte, error) {
	if len(encodedKey) == 0 {
		return encodedKey, nil
	}
	return c.memCodec.decodeKey(encodedKey)
}

func (c *codecV1) EncodeRegionRange(start, end []byte) ([]byte, []byte) {
	return c.EncodeRegionKey(start), c.EncodeRegionKey(end)
}

func (c *codecV1) DecodeRegionRange(encodedStart, encodedEnd []byte) ([]byte, []byte, error) {
	start, err := c.DecodeRegionKey(encodedStart)
	if err != nil {
		return nil, nil, err
	}
	end, err := c.DecodeRegionKey(encodedEnd)
	if err != nil {
		return nil, nil, err
	}
	return start, end, nil
}

func (c *codecV1) decodeRegionError(regionError *errorpb.Error) (*errorpb.Error, error) {
	if regionError == nil {
		return nil, nil
	}
	var err error
	if errInfo := regionError.KeyNotInRegion; errInfo != nil {
		errInfo.StartKey, errInfo.EndKey, err = c.DecodeRegionRange(errInfo.StartKey, errInfo.EndKey)
		if err != nil {
			return nil, err
		}
	}
	if errInfo := regionError.EpochNotMatch; errInfo != nil {
		for _, meta := range errInfo.CurrentRegions {
			meta.StartKey, meta.EndKey, err = c.DecodeRegionRange(meta.StartKey, meta.EndKey)
			if err != nil {
				return nil, err
			}
		}
	}
	return regionError, nil
}
