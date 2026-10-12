-- =============================================================================
-- RESCUE-4 Database Fencing Schema & Preventative Enforcement Protocol
-- Specification: RESCUE-4 v1.5 (PostgreSQL 16.3 Target)
--
-- Enforces:
--   1. Role Isolation: executor_role has NO direct DML permissions on protected tables.
--   2. In-Transaction Fencing: SECURITY DEFINER fenced_write() acquires FOR SHARE lock.
--   3. Bump Serialization: Gateway bump executes conflicting UPDATE on fence row.
--   4. Bypass Backstop: BEFORE triggers reject any direct DML bypassing fenced_write().
-- =============================================================================

-- 1. Create Roles
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'gateway_admin_role') THEN
        CREATE ROLE gateway_admin_role WITH LOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'executor_role') THEN
        CREATE ROLE executor_role WITH LOGIN;
    END IF;
END $$;

-- 2. Core Tables
CREATE TABLE IF NOT EXISTS public.fence (
    gateway_id TEXT PRIMARY KEY,
    inc INT NOT NULL,
    epoch INT NOT NULL,
    seq BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS public.fence_log (
    log_id BIGSERIAL PRIMARY KEY,
    gw TEXT NOT NULL,
    seq BIGINT NOT NULL,
    inc_new INT NOT NULL,
    epoch_new INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS public.audit (
    write_id BIGINT PRIMARY KEY,
    gw TEXT NOT NULL,
    inc INT NOT NULL,
    epoch INT NOT NULL,
    tx_timestamp TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS public.account_ledger (
    write_id BIGINT PRIMARY KEY REFERENCES public.audit(write_id),
    account_id TEXT NOT NULL,
    delta NUMERIC(18, 4) NOT NULL,
    balance NUMERIC(18, 4) NOT NULL
);

CREATE TABLE IF NOT EXISTS public.resource_registry (
    write_id BIGINT PRIMARY KEY REFERENCES public.audit(write_id),
    resource_id TEXT NOT NULL,
    status TEXT NOT NULL
);

-- 3. Security & Permission Revocations
REVOKE ALL ON public.fence FROM executor_role;
REVOKE ALL ON public.fence_log FROM executor_role;
REVOKE ALL ON public.audit FROM executor_role;
REVOKE ALL ON public.account_ledger FROM executor_role;
REVOKE ALL ON public.resource_registry FROM executor_role;

GRANT SELECT ON public.fence TO executor_role;

-- 4. Trigger Function for Bypass Prevention
CREATE OR REPLACE FUNCTION public.trg_assert_fenced_context()
RETURNS TRIGGER AS $$
BEGIN
    IF current_setting('rescue.in_fenced_write', true) <> 'on' THEN
        RAISE EXCEPTION 'DIRECT_DML_DISALLOWED: Direct mutation on protected table % is forbidden without fenced_write()', TG_TABLE_NAME
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_ledger_bypass ON public.account_ledger;
CREATE TRIGGER trg_ledger_bypass
    BEFORE INSERT OR UPDATE OR DELETE ON public.account_ledger
    FOR EACH ROW EXECUTE FUNCTION public.trg_assert_fenced_context();

DROP TRIGGER IF EXISTS trg_resource_bypass ON public.resource_registry;
CREATE TRIGGER trg_resource_bypass
    BEFORE INSERT OR UPDATE OR DELETE ON public.resource_registry
    FOR EACH ROW EXECUTE FUNCTION public.trg_assert_fenced_context();

-- 5. Preventative Enforcement: SECURITY DEFINER fenced_write()
CREATE OR REPLACE FUNCTION public.fenced_write(
    p_gw TEXT,
    p_inc INT,
    p_epoch INT,
    p_write_id BIGINT,
    p_target_table TEXT,
    p_key TEXT,
    p_val NUMERIC
) RETURNS JSONB AS $$
DECLARE
    v_cur_inc INT;
    v_cur_epoch INT;
    v_audit_time TIMESTAMPTZ;
BEGIN
    -- Enable fenced context for triggers
    PERFORM set_config('rescue.in_fenced_write', 'on', true);

    -- 1. In-Transaction Fencing Check with FOR SHARE row lock
    SELECT inc, epoch INTO v_cur_inc, v_cur_epoch
    FROM public.fence
    WHERE gateway_id = p_gw
    FOR SHARE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'UNKNOWN_GATEWAY: Gateway % not recognized in fencing registry', p_gw
            USING ERRCODE = '28000';
    END IF;

    -- Strict lexicographic token verification: (p_inc, p_epoch) == (v_cur_inc, v_cur_epoch)
    IF (p_inc, p_epoch) < (v_cur_inc, v_cur_epoch) THEN
        RAISE EXCEPTION 'STALE_FENCE_TOKEN: Write aborted. Caller token (%, %) is stale against active fence (%, %)',
            p_inc, p_epoch, v_cur_inc, v_cur_epoch
            USING ERRCODE = '55000';
    ELSIF (p_inc, p_epoch) > (v_cur_inc, v_cur_epoch) THEN
        RAISE EXCEPTION 'FUTURE_FENCE_TOKEN: Write aborted. Caller token (%, %) exceeds current active fence (%, %)',
            p_inc, p_epoch, v_cur_inc, v_cur_epoch
            USING ERRCODE = '55001';
    END IF;

    -- 2. Correlated Audit Row Insertion
    v_audit_time := clock_timestamp();
    INSERT INTO public.audit (write_id, gw, inc, epoch, tx_timestamp)
    VALUES (p_write_id, p_gw, p_inc, p_epoch, v_audit_time);

    -- 3. Data Table Mutation
    IF p_target_table = 'account_ledger' THEN
        INSERT INTO public.account_ledger (write_id, account_id, delta, balance)
        VALUES (p_write_id, p_key, p_val, p_val);
    ELSIF p_target_table = 'resource_registry' THEN
        INSERT INTO public.resource_registry (write_id, resource_id, status)
        VALUES (p_write_id, p_key, 'ACTIVE');
    ELSE
        RAISE EXCEPTION 'INVALID_TARGET_TABLE: Unknown protected table %', p_target_table;
    END IF;

    RETURN jsonb_build_object(
        'status', 'COMMITTED',
        'write_id', p_write_id,
        'gw', p_gw,
        'fence_version', format('(%s, %s)', p_inc, p_epoch),
        'timestamp', v_audit_time
    );
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

GRANT EXECUTE ON FUNCTION public.fenced_write TO executor_role;

-- 6. Gateway Bump Function (Admin Only)
CREATE OR REPLACE FUNCTION public.bump_fence(
    p_gw TEXT,
    p_inc_new INT,
    p_epoch_new INT
) RETURNS JSONB AS $$
DECLARE
    v_cur_inc INT;
    v_cur_epoch INT;
    v_cur_seq BIGINT;
    v_new_seq BIGINT;
BEGIN
    -- Conflict with concurrent FOR SHARE row locks
    SELECT inc, epoch, seq INTO v_cur_inc, v_cur_epoch, v_cur_seq
    FROM public.fence
    WHERE gateway_id = p_gw
    FOR UPDATE;

    IF NOT FOUND THEN
        INSERT INTO public.fence (gateway_id, inc, epoch, seq)
        VALUES (p_gw, p_inc_new, p_epoch_new, 1)
        RETURNING seq INTO v_new_seq;
    ELSE
        IF (p_inc_new, p_epoch_new) <= (v_cur_inc, v_cur_epoch) THEN
            RAISE EXCEPTION 'NON_MONOTONIC_BUMP: Proposed version (%, %) must strictly exceed current (%, %)',
                p_inc_new, p_epoch_new, v_cur_inc, v_cur_epoch;
        END IF;
        v_new_seq := v_cur_seq + 1;
        UPDATE public.fence
        SET inc = p_inc_new, epoch = p_epoch_new, seq = v_new_seq, updated_at = clock_timestamp()
        WHERE gateway_id = p_gw;
    END IF;

    -- Record in fence log for logical decoding replication
    INSERT INTO public.fence_log (gw, seq, inc_new, epoch_new)
    VALUES (p_gw, v_new_seq, p_inc_new, p_epoch_new);

    RETURN jsonb_build_object(
        'status', 'BUMP_APPLIED',
        'gw', p_gw,
        'new_version', format('(%s, %s)', p_inc_new, p_epoch_new),
        'seq', v_new_seq
    );
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

REVOKE EXECUTE ON FUNCTION public.bump_fence FROM executor_role;
GRANT EXECUTE ON FUNCTION public.bump_fence TO gateway_admin_role;
