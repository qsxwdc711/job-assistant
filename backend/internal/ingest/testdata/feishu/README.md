# Public Feishu recruitment fixtures

These files contain public job data only. No applicant account, resume, phone,
cookie, CSRF token, request signature or login state is included.

- `bambulab-list-first.json`: the first public ten-job page observed on 2026-10-22;
  its reported total is a historical snapshot, not a claim that this file contains
  the complete live list. Pagination tests deliberately construct bounded pages.
- `bambulab-frontend.json`: public job `7696343742794549547` at
  `https://bambulab.jobs.feishu.cn/campus/position/7696343742794549547/detail`.
- `bambulab-backend-intern.json`: public job `7690802298373736755` at
  `https://bambulab.jobs.feishu.cn/campus/position/7690802298373736755/detail`.
  This 2026-10-23 sample exercises required versus bonus qualifications.
- `agibot-empty-intern.json`: public job `7625482260637550867`, observed on
  2026-10-24 at
  `https://agirobot.jobs.feishu.cn/campusrecruitment/position/7625482260637550867/detail`.
  The source omits both description and requirement. It must be excluded for
  manual verification without inventing conditions or publishing a partial
  snapshot. A field with the wrong JSON type remains a schema failure.

The company's own careers page directly authorizes each tenant. Complete live
reads and their response hashes are recorded separately under ignored research
evidence; transport timestamps do not participate in normalized content identity.
