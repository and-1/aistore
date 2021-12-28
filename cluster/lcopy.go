// Package cluster provides common interfaces and local access to cluster-level metadata
/*
 * Copyright (c) 2018-2021, NVIDIA CORPORATION. All rights reserved.
 */
package cluster

import (
	"fmt"
	"os"

	"github.com/NVIDIA/aistore/3rdparty/glog"
	"github.com/NVIDIA/aistore/cmn"
	"github.com/NVIDIA/aistore/cmn/cos"
	"github.com/NVIDIA/aistore/cmn/debug"
	"github.com/NVIDIA/aistore/fs"
)

//
// LOM copy management
//

func (lom *LOM) whingeCopy() (yes bool) {
	if !lom.IsCopy() {
		return
	}
	msg := fmt.Sprintf("unexpected: %s([fqn=%s] [hrw=%s] %+v)", lom, lom.FQN, lom.HrwFQN, lom.md.copies)
	debug.AssertMsg(false, msg)
	glog.Error(msg)
	return true
}

func (lom *LOM) HasCopies() bool { return lom.NumCopies() > 1 }
func (lom *LOM) NumCopies() int  { return cos.Max(len(lom.md.copies), 1) } // TODO: compare with `gotCopies` and unify

// GetCopies returns all copies (NOTE that copies include self)
// NOTE: caller must take a lock
func (lom *LOM) GetCopies() fs.MPI {
	debug.AssertFunc(func() bool {
		rc, exclusive := lom.IsLocked()
		return exclusive || rc > 0
	})
	return lom.md.copies
}

// given an existing (on-disk) object, determines whether it is a _copy_
// (compare with isMirror below)
func (lom *LOM) IsCopy() bool {
	if lom.IsHRW() {
		return false
	}
	// misplaced or a copy
	_, ok := lom.md.copies[lom.FQN]
	return ok
}

// determines whether the two LOM _structures_ represent objects that must be _copies_ of each other
// (compare with IsCopy above)
func (lom *LOM) isMirror(dst *LOM) bool {
	return lom.MirrorConf().Enabled &&
		lom.ObjName == dst.ObjName &&
		lom.Bck().Equal(dst.Bck(), true /* must have same BID*/, true /* same backend */)
}

func (lom *LOM) delCopyMd(copyFQN string) {
	delete(lom.md.copies, copyFQN)
	if len(lom.md.copies) <= 1 {
		lom.md.copies = nil
	}
}

// NOTE: used only in tests
func (lom *LOM) AddCopy(copyFQN string, mpi *fs.MountpathInfo) error {
	if lom.md.copies == nil {
		lom.md.copies = make(fs.MPI, 2)
	}
	lom.md.copies[copyFQN] = mpi
	lom.md.copies[lom.FQN] = lom.mpathInfo
	return lom.syncMetaWithCopies()
}

func (lom *LOM) DelCopies(copiesFQN ...string) (err error) {
	numCopies := lom.NumCopies()
	// 1. Delete all copies from the metadata
	for _, copyFQN := range copiesFQN {
		if _, ok := lom.md.copies[copyFQN]; !ok {
			return fmt.Errorf("lom %s(num: %d): copy %s does not exist", lom, numCopies, copyFQN)
		}
		lom.delCopyMd(copyFQN)
	}

	// 2. Update metadata on remaining copies, if any
	if err := lom.syncMetaWithCopies(); err != nil {
		debug.AssertNoErr(err)
		return err
	}

	// 3. Remove the copies
	for _, copyFQN := range copiesFQN {
		if err1 := cos.RemoveFile(copyFQN); err1 != nil {
			glog.Error(err1) // TODO: LRU should take care of that later.
			continue
		}
	}
	return
}

func (lom *LOM) DelAllCopies() (err error) {
	copiesFQN := make([]string, 0, len(lom.md.copies))
	for copyFQN := range lom.md.copies {
		if copyFQN == lom.FQN {
			continue
		}
		copiesFQN = append(copiesFQN, copyFQN)
	}
	return lom.DelCopies(copiesFQN...)
}

// DelExtraCopies deletes obj replicas that are not part of the lom.md.copies metadata
// (cleanup)
func (lom *LOM) DelExtraCopies(fqn ...string) (removed bool, err error) {
	if lom.whingeCopy() {
		return
	}
	availablePaths := fs.GetAvail()
	for _, mi := range availablePaths {
		copyFQN := mi.MakePathFQN(lom.Bucket(), fs.ObjectType, lom.ObjName)
		if _, ok := lom.md.copies[copyFQN]; ok {
			continue
		}
		if err1 := cos.RemoveFile(copyFQN); err1 != nil {
			err = err1
			continue
		}
		if len(fqn) > 0 && fqn[0] == copyFQN {
			removed = true
		}
	}
	return
}

// syncMetaWithCopies tries to make sure that all copies have identical metadata.
// NOTE: uname for LOM must be already locked.
// NOTE: changes _may_ be made - the caller must call lom.Persist() upon return
func (lom *LOM) syncMetaWithCopies() (err error) {
	var copyFQN string
	if !lom.HasCopies() {
		return nil
	}
	// NOTE: caller is responsible for write-locking
	debug.AssertFunc(func() bool {
		_, exclusive := lom.IsLocked()
		return exclusive
	})
	if !lom.WritePolicy().IsImmediate() {
		lom.md.makeDirty()
		return nil
	}
	for {
		if copyFQN, err = lom.persistMdOnCopies(); err == nil {
			break
		}
		lom.delCopyMd(copyFQN)
		if err1 := fs.Access(copyFQN); err1 != nil && !os.IsNotExist(err1) {
			T.FSHC(err, copyFQN) // TODO: notify scrubber
		}
	}
	return
}

// RestoreObjectFromAny tries to restore the object at its default location.
// Returns true if object exists, false otherwise
// TODO: locking vs concurrent restore: consider (read-lock object + write-lock meta) split
func (lom *LOM) RestoreObjectFromAny() (exists bool) {
	lom.Lock(true)
	if err := lom.Load(true /*cache it*/, true /*locked*/); err == nil {
		lom.Unlock(true)
		return true // nothing to do
	}
	availablePaths := fs.GetAvail()
	buf, slab := T.PageMM().Alloc()
	for path, mi := range availablePaths {
		if path == lom.mpathInfo.Path {
			continue
		}
		fqn := mi.MakePathFQN(lom.Bucket(), fs.ObjectType, lom.ObjName)
		if _, err := os.Stat(fqn); err != nil {
			continue
		}
		dst, err := lom._restore(fqn, buf)
		if err == nil {
			lom.md = dst.md
			exists = true
			FreeLOM(dst)
			break
		}
		if dst != nil {
			FreeLOM(dst)
		}
	}
	lom.Unlock(true)
	slab.Free(buf)
	return
}

func (lom *LOM) _restore(fqn string, buf []byte) (dst *LOM, err error) {
	src := lom.Clone(fqn)
	defer FreeLOM(src)
	if err = src.Init(lom.Bucket()); err != nil {
		return
	}
	if err = src.Load(false /*cache it*/, true /*locked*/); err != nil {
		return
	}
	// restore at default location
	dst, err = src.Copy2FQN(lom.FQN, buf)
	return
}

// increment the object's num copies by (well) copying the former
// (compare with lom.Copy2FQN below)
func (lom *LOM) Copy(mi *fs.MountpathInfo, buf []byte) (err error) {
	var (
		copyFQN = mi.MakePathFQN(lom.Bucket(), fs.ObjectType, lom.ObjName)
		workFQN = mi.MakePathFQN(lom.Bucket(), fs.WorkfileType, fs.WorkfileCopy+"."+lom.ObjName)
	)
	// check if the copy destination exists and then skip copying if it's also identical
	if _, errExists := os.Stat(copyFQN); errExists == nil {
		cplom := AllocLOMbyFQN(copyFQN)
		defer FreeLOM(cplom)
		if errExists = cplom.Init(lom.Bucket()); errExists == nil {
			if errExists = cplom.Load(false /*cache it*/, true /*locked*/); errExists == nil && cplom.Equal(lom) {
				goto add
			}
		}
	}

	// copy
	_, _, err = cos.CopyFile(lom.FQN, workFQN, buf, cos.ChecksumNone) // TODO: checksumming
	if err != nil {
		return
	}
	if err = cos.Rename(workFQN, copyFQN); err != nil {
		if errRemove := cos.RemoveFile(workFQN); errRemove != nil {
			glog.Errorf(fmtNestedErr, errRemove)
		}
		return
	}
add:
	// add md and persist
	lom.AddCopy(copyFQN, mi)
	err = lom.Persist()
	if err != nil {
		lom.delCopyMd(copyFQN)
		glog.Error(err)
		return err
	}
	err = lom.syncMetaWithCopies()
	return
}

// copy object => any local destination
// recommended for copying between different buckets (compare with lom.Copy() above)
// NOTE: `lom` source must be w-locked
func (lom *LOM) Copy2FQN(dstFQN string, buf []byte) (dst *LOM, err error) {
	var (
		dstCksum  *cos.CksumHash
		srcCksum  = lom.Checksum()
		cksumType = cos.ChecksumNone
	)
	if !srcCksum.IsEmpty() {
		cksumType = srcCksum.Ty()
	}
	dst = lom.Clone(dstFQN)
	if err = dst.Init(cmn.Bck{}); err != nil {
		return
	}
	dst.md.copies = nil
	if dst.isMirror(lom) {
		// caller must take wlock
		debug.AssertFunc(func() bool {
			_, exclusive := lom.IsLocked()
			return exclusive
		})
		if lom.md.copies != nil {
			dst.md.copies = make(fs.MPI, len(lom.md.copies)+1)
			for fqn, mpi := range lom.md.copies {
				dst.md.copies[fqn] = mpi
			}
		}
	}

	if !dst.Bucket().Equal(lom.Bucket()) {
		// The copy will be in a new bucket - completely separate object. Hence, we have to set initial version.
		dst.SetVersion(lomInitialVersion)
	}

	workFQN := fs.CSM.Gen(dst, fs.WorkfileType, fs.WorkfileCopy)
	_, dstCksum, err = cos.CopyFile(lom.FQN, workFQN, buf, cksumType)
	if err != nil {
		return
	}

	if err = cos.Rename(workFQN, dstFQN); err != nil {
		if errRemove := cos.RemoveFile(workFQN); errRemove != nil {
			glog.Errorf(fmtNestedErr, errRemove)
		}
		return
	}

	if cksumType != cos.ChecksumNone {
		if !dstCksum.Equal(lom.Checksum()) {
			return nil, cos.NewBadDataCksumError(&dstCksum.Cksum, lom.Checksum())
		}
		dst.SetCksum(dstCksum.Clone())
	}

	// persist
	if lom.isMirror(dst) {
		if lom.md.copies == nil {
			lom.md.copies = make(fs.MPI, 2)
			dst.md.copies = make(fs.MPI, 2)
		}
		lom.md.copies[dstFQN], dst.md.copies[dstFQN] = dst.mpathInfo, dst.mpathInfo
		lom.md.copies[lom.FQN], dst.md.copies[lom.FQN] = lom.mpathInfo, lom.mpathInfo
		if err = lom.syncMetaWithCopies(); err != nil {
			if _, ok := lom.md.copies[dst.FQN]; !ok {
				if errRemove := os.Remove(dst.FQN); errRemove != nil {
					glog.Errorf("nested err: %v", errRemove)
				}
			}
			// `lom.syncMetaWithCopies()` may have made changes notwithstanding
			if errPersist := lom.Persist(); errPersist != nil {
				glog.Errorf("nested err: %v", errPersist)
			}
			return
		}
		err = lom.Persist()
	} else if err = dst.Persist(); err != nil {
		if errRemove := os.Remove(dst.FQN); errRemove != nil {
			glog.Errorf("nested err: %v", errRemove)
		}
	}
	return
}

// load-balanced GET
func (lom *LOM) LBGet() (fqn string) {
	if !lom.HasCopies() {
		return lom.FQN
	}
	return lom.leastUtilCopy()
}

// NOTE: reconsider counting GETs (and the associated overhead)
//       vs ios.refreshIostatCache() (and the associated delay)
func (lom *LOM) leastUtilCopy() (fqn string) {
	var (
		mpathUtils = fs.GetAllMpathUtils()
		minUtil    = mpathUtils.Get(lom.mpathInfo.Path)
		copies     = lom.GetCopies()
	)
	fqn = lom.FQN
	for copyFQN, copyMPI := range copies {
		if copyFQN != lom.FQN {
			if util := mpathUtils.Get(copyMPI.Path); util < minUtil {
				fqn, minUtil = copyFQN, util
			}
		}
	}
	return
}

// returns the least utilized mountpath that does _not_ have a copy of this `lom` yet
// (compare with leastUtilCopy())
func (lom *LOM) LeastUtilNoCopy() (mi *fs.MountpathInfo) {
	var (
		availablePaths = fs.GetAvail()
		mpathUtils     = fs.GetAllMpathUtils()
		minUtil        = int64(101) // to motivate the first assignment
	)
	for mpath, mpathInfo := range availablePaths {
		if lom.haveMpath(mpath) || mpathInfo.IsAnySet(fs.FlagWaitingDD) {
			continue
		}
		if util := mpathUtils.Get(mpath); util < minUtil {
			minUtil, mi = util, mpathInfo
		}
	}
	return
}

func (lom *LOM) haveMpath(mpath string) bool {
	if len(lom.md.copies) == 0 {
		return lom.mpathInfo.Path == mpath
	}
	for _, mi := range lom.md.copies {
		if mi.Path == mpath {
			return true
		}
	}
	return false
}

// must be called under w-lock
// returns mountpath destination to copy this object, or nil if no copying is required
// - checks hrw location first, and
// - checks copies (if any) against the current configuation and available mountpaths;
// - does not check `fstat` in either case (TODO: configurable or scrub);
func (lom *LOM) ToMpath() (mi *fs.MountpathInfo, isHrw bool) {
	var (
		availablePaths = fs.GetAvail()
		hrwMi, _, err  = HrwMpath(lom.md.uname)
	)
	if err != nil {
		glog.Error(err)
		return
	}
	debug.Assert(!hrwMi.IsAnySet(fs.FlagWaitingDD))
	if lom.mpathInfo.Path != hrwMi.Path {
		return hrwMi, true
	}
	mirror := lom.MirrorConf()
	if !mirror.Enabled || mirror.Copies < 2 {
		return
	}
	// count copies vs. configuration
	// take into account mountpath flags but stop short of `fstat`-ing
	expCopies, gotCopies := int(mirror.Copies), 0
	for fqn, mpi := range lom.md.copies {
		mpathInfo, ok := availablePaths[mpi.Path]
		if !ok || mpathInfo.IsAnySet(fs.FlagWaitingDD) {
			lom.delCopyMd(fqn)
		} else {
			gotCopies++
		}
	}
	if expCopies <= gotCopies {
		return
	}
	mi = lom.LeastUtilNoCopy()
	if mi == nil && glog.FastV(4, glog.SmoduleCluster) {
		glog.Warningf("%s: not enough mountpaths (%d) to place (%d/%d) copies",
			lom, len(availablePaths), gotCopies, expCopies)
	}
	return
}