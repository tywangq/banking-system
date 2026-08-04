-- A transfer must not be able to drive an account negative.
--
-- Checking the balance in application code before the transfer is not enough:
-- two concurrent transfers can both read a sufficient balance and both proceed.
-- Only the database sees the serialized result, so the guarantee belongs here.
ALTER TABLE "accounts"
  ADD CONSTRAINT "accounts_balance_non_negative" CHECK ("balance" >= 0);
