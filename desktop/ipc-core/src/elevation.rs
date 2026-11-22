//! Refuse-administrator decision. Same rule as the Go side (`elevationDecision` in cmd/audit-explorer/gate_windows.go, spec 01 D13):
//! only a FULL token is refused, and then only when it is flagged elevated or its integrity level is High or above. Token type
//! "default" (UAC off, or the built-in Administrator) runs, whatever its flag. An unreadable token counts as elevated.

/// Facts the shell reads from its own token (the Windows code fills this in).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TokenFacts {
    /// `TokenElevationType` is `TokenElevationTypeFull`.
    pub full_token: bool,
    /// `TokenElevation.TokenIsElevated` is non-zero.
    pub elevated: bool,
    /// The token's integrity level is High (0x3000) or above.
    pub integrity_high: bool,
}

impl TokenFacts {
    /// What the shell uses when any token read fails.
    pub const UNREADABLE: TokenFacts = TokenFacts { full_token: true, elevated: true, integrity_high: true };
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Decision {
    Run,
    Refuse,
}

/// Exit code of the shell when it refuses (spec 01: 77 = administrator).
pub const EXIT_ADMIN: i32 = 77;

pub const MESSAGE: &str = "Unravis does not run as administrator. Start it from a normal window.";

pub fn decide(f: TokenFacts) -> Decision {
    if f.full_token && (f.elevated || f.integrity_high) { Decision::Refuse } else { Decision::Run }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn f(full_token: bool, elevated: bool, integrity_high: bool) -> TokenFacts {
        TokenFacts { full_token, elevated, integrity_high }
    }

    #[test]
    fn elevation_decision_table() {
        assert_eq!(decide(f(false, false, false)), Decision::Run, "standard user");
        assert_eq!(decide(f(false, false, false)), Decision::Run, "admin with UAC, filtered (limited) token");
        assert_eq!(decide(f(false, true, true)), Decision::Run, "UAC off or built-in Administrator: type default, runs like the Go side");
        assert_eq!(decide(f(true, true, true)), Decision::Refuse, "elevated full token");
        assert_eq!(decide(f(true, true, false)), Decision::Refuse, "full token flagged elevated");
        assert_eq!(decide(f(true, false, true)), Decision::Refuse, "full token at high integrity");
        assert_eq!(decide(f(true, false, false)), Decision::Run, "full token that is neither flagged nor high");
    }

    #[test]
    fn unreadable_token_is_refused() {
        assert_eq!(decide(TokenFacts::UNREADABLE), Decision::Refuse);
    }
}
