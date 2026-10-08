ALTER TABLE reward_rules ADD COLUMN reward_usd numeric(18,4) NOT NULL DEFAULT 0;
ALTER TABLE reward_rules ADD COLUMN max_users integer NOT NULL DEFAULT 0 CHECK (max_users >= 0);
ALTER TABLE reward_rules ALTER COLUMN reward_pkr SET DEFAULT 0;
ALTER TABLE reward_rules DROP CONSTRAINT IF EXISTS reward_rules_reward_pkr_check;
ALTER TABLE reward_rules ADD CONSTRAINT reward_rules_amount_check
    CHECK (reward_pkr >= 0 AND reward_usd >= 0 AND (reward_pkr > 0 OR reward_usd > 0));

ALTER TABLE reward_awards ADD COLUMN amount_usd numeric(18,4) NOT NULL DEFAULT 0;
ALTER TABLE user_daily_progress ADD COLUMN reward_earnings_usd numeric(18,4) NOT NULL DEFAULT 0;
