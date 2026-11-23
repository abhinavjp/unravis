// schema_sha256: 2a629ba3ba6dcb318379bf3dfaa6bbec25a99369686a65648be2780cce197bbd
// test

declare namespace unravis {
  type AuditEmptyInput = Record<string, never>;
  type AuditEmptyOutput = unknown;
  type AuditXInput = {
    deep?: unknown;
    /**
     * Connected source ids
     * [minItems 1, maxItems 8]
     */
    ids: string[];
    /**
     * [minimum 1, maximum 100]
     */
    limit?: number;
    mix?: string | number;
    mode: "a" | "b";
    pat?: Record<string, unknown>;
    range?: {
      max?: number;
      min?: number;
    };
    tuple?: unknown[];
    "weird-name"?: string | null;
    /**
     * [format date-time]
     */
    when?: string;
  };
  type AuditXOutput = AuditXOutputFailure | AuditXOutputSuccess;
  type AuditXOutputBase = {
    cut?: null | {
      n?: number;
    };
    error?: {
      code: string;
    };
    next?: string | null;
    partial: boolean;
    rows?: Row[];
    tool: "x";
    [key: string]: unknown;
  };
  /**
   * The call failed: error is set and the tool's own fields are absent.
   */
  type AuditXOutputFailure = AuditXOutputBase & Required<Pick<AuditXOutputBase, "error">>;
  /**
   * The call worked: the fields the tool always returns are set.
   */
  type AuditXOutputSuccess = AuditXOutputBase & Required<Pick<AuditXOutputBase, "rows">> & { error?: never };
  type Row = {
    child?: Row;
    id: string;
  };

  /**
   * Does x.
   * Second line.
   * Limit: 4 in flight.
   */
  function audit_x(input: AuditXInput): Promise<AuditXOutput>;
  /**
   * No arguments.
   */
  function audit_empty(input?: AuditEmptyInput): Promise<AuditEmptyOutput>;
}
