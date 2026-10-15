export interface User { id: string; username: string; role: string; vip_expires_at: string | null; disabled: boolean }
export interface Identity { user: User; effective_role: string; capabilities: Record<string, boolean>; csrf_token: string }
export interface Place { code: string; kind: string; country: string; province: string; city: string; raw: string }
export interface Variant {
  key: string; name: string; direction: string; responsibilities: string; requirements: string; preferred: string
  major: { kind: string; tags: string[]; evidence: string }
  locations: Place[]; departments: { code: string; name: string; locations: Place[] }[]
}
export interface Job {
  id: string; company_code: string; company_name: string; industry: string; nature: string; tiers: string[]
  company_basis: string; source_code: string; campaign_name: string; title: string; recruitment_type: string
  public_status: string; status_evidence: string; verification_status: string; written_test: string
  written_test_evidence: string; source_url: string; apply_url: string; apply_level: string; login_required: boolean
  deadline_date: string; deadline_at: string | null; deadline_timezone: string; publisher: string
  graduation_rules: {region: string; education: string; from: string; through: string; evidence: string}[]
  graduation_preference: string; student_required: string; minimum_education: string
  minimum_internship_months: number | null; days_per_week: number | null; campaign_evidence: string
  variants: Variant[]; version: number; published: boolean; review_note: string; verified_at: string; updated_at: string
}
export interface Company { code: string; name: string; industry: string; nature: string; tiers: string[]; basis: string }
export interface Choices { dictionary: {kind: string; code: string; name: string}[]; cities: Place[]; companies: Company[] }
export interface Page<T> { items: T[]; total: number; limit: number; offset: number }
export interface JobState { job_id: string; favorite: boolean; recorded: boolean }
export interface Application {
  id: string; owner_user_id: string; owner_username: string; job_id: string; status: string; notes: string
  next_action: string; next_action_at: string | null; version: number; created_at: string; updated_at: string; job: Job
}
export interface ApplicationEvent { id: string; status: string; notes: string; next_action: string; next_action_at: string | null; version: number; created_at: string }
export interface Source {
  code: string; name: string; coverage: string; official_url: string; enabled: boolean; last_status: string
  last_error: string; last_checked_at: string | null; last_succeeded_at: string | null; job_count: number
}
