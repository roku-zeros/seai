CREATE TABLE tasks (
 id TEXT PRIMARY KEY,
 alias TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('explain','change','refactor')),
 state TEXT NOT NULL CHECK(state IN ('SPEC_READY','PLANNED','IMPLEMENTING','CANCELLED','NEEDS_INPUT')),
 revision INTEGER NOT NULL CHECK(revision>0),
 baseline_id TEXT NOT NULL REFERENCES source_snapshots(id),
 candidate_id TEXT NOT NULL REFERENCES source_snapshots(id),
 baseline_epoch INTEGER NOT NULL,
 spec_digest TEXT NOT NULL,
 behavior_digest TEXT NOT NULL,
 plan_digest TEXT,
 approved_digest TEXT,
 checkpoint TEXT NOT NULL DEFAULT 'SPEC_READY'
);
CREATE TABLE task_specs(task_id TEXT NOT NULL REFERENCES tasks(id),revision INTEGER NOT NULL,digest TEXT NOT NULL,PRIMARY KEY(task_id,revision));
CREATE TABLE decisions(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),behavior_digest TEXT NOT NULL,spec_digest TEXT NOT NULL,actor TEXT NOT NULL,channel TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE operations(operation_id TEXT PRIMARY KEY,method TEXT NOT NULL,payload_digest TEXT NOT NULL,result_json TEXT NOT NULL);
CREATE TABLE patches(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),base_id TEXT NOT NULL REFERENCES source_snapshots(id),candidate_id TEXT NOT NULL REFERENCES source_snapshots(id),manifest_digest TEXT NOT NULL);

INSERT INTO operations(operation_id,method,payload_digest,result_json)
 SELECT id,'index.build',payload_digest,json_object('job_id',id,'state','QUEUED') FROM jobs;
