//! Refuse-administrator decision (BL-304 on the Go side, BL-306 here). The shell never runs elevated: a process with an admin token
//! can be steered by anything that talks to its WebView, and the data lives per user.

/// Facts the shell reads from its own token (the Windows code fills this in).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TokenFacts {
    /// `TokenElevation.TokenIsElevated` is non-zero.
    pub elevated: bool,
    /// The user is in the Administrators group but the token is the filtered one (normal UAC).
    pub admin_member_filtered: bool,
    /// UAC is off (`EnableLUA` = 0) and the user is an administrator: every process is full-admin.
    pub uac_off_admin: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Decision {
    Run,
    Refuse(Reason),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Reason {
    Elevated,
    UacOffAdmin,
}

/// Exit code of the shell when it refuses (spec 01: 77 = administrator).
pub const EXIT_ADMIN: i32 = 77;

pub fn decide(f: TokenFacts) -> Decision {
    if f.uac_off_admin {
        return Decision::Refuse(Reason::UacOffAdmin);
    }
    if f.elevated {
        return Decision::Refuse(Reason::Elevated);
    }
    // A filtered admin token is the normal case on a UAC machine: it runs as a standard user.
    Decision::Run
}

pub fn message(r: Reason) -> &'static str {
    match r {
        Reason::Elevated => "Unravis does not run as administrator. Start it from a normal window.",
        Reason::UacOffAdmin => {
            "Unravis does not run when every program has administrator rights (UAC is off). Turn UAC on or use a standard user."
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn f(elevated: bool, admin_member_filtered: bool, uac_off_admin: bool) -> TokenFacts {
        TokenFacts { elevated, admin_member_filtered, uac_off_admin }
    }

    #[test]
    fn elevation_decision_table() {
        assert_eq!(decide(f(false, false, false)), Decision::Run, "standard user");
        assert_eq!(decide(f(false, true, false)), Decision::Run, "admin with UAC, filtered token");
        assert_eq!(decide(f(true, false, false)), Decision::Refuse(Reason::Elevated));
        assert_eq!(decide(f(true, true, false)), Decision::Refuse(Reason::Elevated));
        assert_eq!(decide(f(false, false, true)), Decision::Refuse(Reason::UacOffAdmin));
        assert_eq!(decide(f(true, false, true)), Decision::Refuse(Reason::UacOffAdmin), "UAC off: the more specific message");
    }

    #[test]
    fn every_reason_has_a_message() {
        assert!(message(Reason::Elevated).contains("administrator"));
        assert!(message(Reason::UacOffAdmin).contains("UAC"));
    }
}
