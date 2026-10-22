# 2026-10-09 — GraphQL: the HTTP path before and after `httpclient.Send`

The GraphQL module (ADR-021) reuses HTTP's request code by splitting
`httpclient.Do` into `Send` (build, send, time, read the body) and the
HTTP recording. This run checks that the split costs the HTTP path
nothing.

## Environment

| Item | Value |
|---|---|
| Machine | 12th Gen Intel Core i5-1235U (10 cores / 12 threads), 15.7 GB, Windows 11 Enterprise 10.0.26300 |
| Power / isolation | **On AC power** (`PowerOnline: True`); no other work was started |
| Builds | **base:** `phase-2-protocol-breadth` at `de13035`; **new:** branch `p2/graphql` (working tree) |
| Go | go1.27.0 windows/amd64 |

## Method

**Test binaries** of `internal/httpclient` and `internal/script` were
built from both trees.

**Five rounds,** alternating base and new:

- `BenchmarkDo`: one HTTP request through `httpclient.Do`;
- `BenchmarkIterateHTTPGet`: one VU iteration making one `http.get`.

Both ran against a local test server, with `-benchmem`.

## Measured

| Round | Build | `Do` ns/op | B/op | allocs/op | `IterateHTTPGet` ns/op | B/op | allocs/op |
|---|---|---|---|---|---|---|---|
| 1 | base | 81,046 | 5,752 | 69 | 84,758 | 6,503 | 81 |
| 1 | new | 83,757 | 5,749 | 69 | 95,215 | 6,496 | 81 |
| 2 | base | 91,727 | 5,756 | 69 | 82,761 | 6,506 | 81 |
| 2 | new | 74,737 | 5,748 | 69 | 81,596 | 6,496 | 81 |
| 3 | base | 77,563 | 5,751 | 69 | 86,669 | 6,508 | 81 |
| 3 | new | 71,665 | 5,746 | 69 | 83,028 | 6,500 | 81 |
| 4 | base | 74,777 | 5,749 | 69 | 82,428 | 6,504 | 81 |
| 4 | new | 69,712 | 5,752 | 69 | 79,001 | 6,505 | 81 |
| 5 | base | 70,957 | 5,742 | 69 | 77,567 | 6,497 | 81 |
| 5 | new | 69,265 | 5,742 | 69 | 76,380 | 6,498 | 81 |

## Conclusion

**No measurable change to the HTTP path:**

- **Allocations are identical:** 69 and 81 per operation.
- **Bytes per operation** differ by a few bytes, with no direction.
- **Times overlap, with no direction either:**
  - `Do`: 70,957–91,727 ns before, 69,265–83,757 ns after;
  - `IterateHTTPGet`: 77,567–86,669 ns before, 76,380–95,215 ns after.

**Caveats:** one laptop, five rounds, local loopback requests.
