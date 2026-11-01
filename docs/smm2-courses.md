# Super Mario Maker 2 course storage

`switch-super-mario-maker-2` stores uploaded courses durably when `SMM2_DATABASE_URL`
is set. Unset, it behaves as before (profiles in memory, empty Course World lists).

## Where things live

| What | Where |
| --- | --- |
| Makers (profile, Mii, maker code) | Postgres `smm2.users` (database `smm2`) |
| Courses (metadata, stats, status, the raw upload parameter) | `smm2.courses`, ids from `smm2.course_ids` (start 1000000) |
| Thumbnails and replays | `smm2.relations` (size per relation type), bytes in the bucket |
| Level body (`.bcd`) | MinIO bucket `nex-smm2`, key `course/<dataId>.bcd` |
| Thumbnail / replay | `relation/<dataId>/<type>` (1 one-screen, 2 entire, 3 report, 5 replay) |

The schema is created on start. The console never talks to Postgres or MinIO through this
process: it POSTs the body to a presigned S3 form and GETs through presigned URLs, both signed
(SigV4, `internal/smm2store/s3.go`, checked against AWS's published examples) for
`SMM2_S3_PUBLIC_URL`, the host the console resolves.

## Methods

Numbers and structures are kinnay's NintendoClients `datastore_smm2` (MIT) and its wiki page.

| Method | Behaviour |
| --- | --- |
| 66 `PreparePostObjectCourse` | new pending course (cap 32 per maker), answers `DataStoreReqPostInfo` with the S3 POST form |
| 68 `CompletePostObjectsCourse`, 26 `CompletePostObject` | course becomes visible; body size read back from the bucket |
| 132 `PreparePostRelationObject` | S3 POST form for a thumbnail or replay |
| 25 `PrepareGetObject` | presigned GET for a visible course body |
| 4 `DeleteObject` | owner only; the course disappears from every list |
| 70 `GetCourses`, 73 Latest, 72 Advanced (newest), 74 PostedBy, 71 PointRanking (hearts) | `CourseInfo` lists; thumbnails are presigned GET URLs inside `CourseInfo` |
| 47/48/49 | profiles, now durable; `UserInfo` carries the real "n/32" count |

Course codes are `XXX-XXX-XXX`: a 36-bit data id plus an 8-bit checksum in base 30
(`SMM2CourseCode`). They are OpenPak's own and do not match Nintendo's.

## Inferred, not console-measured

* `PreparePostCourseParam` is mostly "Unknown" in the wiki. The name and description are
  taken from its first two strings, style and theme from the two bytes after the bool, clear
  condition and magnitude from the later integers, following the order of `CourseInfo`.
  The whole parameter is stored in `smm2.courses.raw`, so the first capture can correct the
  mapping and existing rows can be re-read.
* The first u32 of `PreparePostRelationObjectParam` is taken as the relation type and the
  second as its size.
* The trailing bool of the search answers is `true`, as the earlier empty answers sent.
* Nothing here has seen a console. No hearts, plays or clears are recorded yet (the methods
  that report them are not served), so those counters stay 0. Next: 131 `GetUserOrCourse`
  (find by code), rating and play-result reporting, comments, bookmarks.

## Configuration

`SMM2_DATABASE_URL`, `SMM2_S3_ENDPOINT` (dialled by this process), `SMM2_S3_PUBLIC_URL`
(told to consoles), `SMM2_S3_BUCKET` (default `nex-smm2`), `SMM2_S3_ACCESS_KEY`,
`SMM2_S3_SECRET_KEY`, `SMM2_API_ADDR` and `SMM2_API_KEY` (read API).

## Read API (for the course site)

`GET /v1/courses?sort=newest|stars|plays&q=&maker=&limit=&offset=&updated_since=`,
`/v1/courses/{code}`, `/v1/courses/{code}/thumbnail`, `/v1/makers`, `/v1/makers/{makerCode}`,
all with header `X-Internal-Key`. Same contract as the Super Mario Maker (Wii U) title; see
the `mario-maker` repo's `docs/api-contract.md`.

## Tests

`go test ./protocols ./internal/smm2store`. The Postgres tests run when
`SMM2_TEST_DATABASE_URL` points at a scratch database.
