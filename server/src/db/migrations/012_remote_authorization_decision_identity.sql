ALTER TABLE remote_authorization_requests
  ALTER COLUMN decision_id SET DEFAULT gen_random_uuid();

UPDATE remote_authorization_requests
   SET decision_id = gen_random_uuid()
 WHERE decision_id IS NULL;

ALTER TABLE remote_authorization_requests
  ALTER COLUMN decision_id SET NOT NULL;
