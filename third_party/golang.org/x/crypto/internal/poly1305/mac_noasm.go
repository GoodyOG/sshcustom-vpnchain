// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !gc || purego || amd64 || arm64 || arm || ppc64 || ppc64le || s390x

package poly1305

type mac struct{ macGeneric }
