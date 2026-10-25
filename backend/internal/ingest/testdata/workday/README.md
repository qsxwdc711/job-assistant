# NVIDIA Workday public fixtures

Captured anonymously on 2026-10-22 from the employer-authorized NVIDIAExternalCareerSite. List fixtures are real China + Intern (Fixed Term) / New College Graduate first pages (76 / 11 source totals at observation), not counts of publishable technical roles. Details are original public job fields required by the adapter. Internal job IDs, questionnaire IDs, login state, cookies, public browser initialization and unrelated similar jobs are omitted. Tests explicitly mutate copied fixtures for negative and bounded pagination cases; those mutations are not employer facts.

Official chain: https://www.nvidia.com/en-us/about-nvidia/careers/ -> https://jobs.nvidia.com/careers -> public configured NVIDIAExternalCareerSite at https://nvidia.wd5.myworkdayjobs.com/. CXS base: /wday/cxs/nvidia/NVIDIAExternalCareerSite.

The additional nvidia-intern-offset20 fixture was captured anonymously on 2026-10-24. The public CXS response returned 20 jobs with total=0 and facets=[] on a later page. The first page at that observation reported total=83. Zero on later pages is a metadata sentinel; the reader retains the first total and verifies it and the first-page identities again at the pagination boundary. Prior failed probes are retained in the ignored research cache.
