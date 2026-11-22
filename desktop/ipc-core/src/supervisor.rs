//! Supervisor state machine of the shell (BL-307): when to restart the Go owner the shell itself started, when to give up, when to
//! call it hung. Pure: time comes in as a `Duration` since some fixed start, jitter comes from a function, nothing sleeps. The Windows
//! code feeds it events and carries out the `Action` it returns.
//!
//! Applies only to a spawned child. A shell that attached to an owner somebody else started never builds one.
//!
//! Caller contract:
//! - The first spawn is the caller's own; `new()` starts in `Starting` and returns no action.
//! - After `SpawnAfter(d)` the caller arms a timer and sends `SpawnDue` when it fires; only an `Action::Spawn` answer means spawn.
//!   A Quit in between makes `SpawnDue` answer `Nothing`, so a pending restart is cancelled.
//! - A spawn that fails (CreateProcess error, child gone before it published, no locator in time) is reported as `SpawnFailed`.
//! - Each child's end is reported once, by the wait thread only, as `Exited`. An `Exited` that arrives in `Backoff` is ignored.

use std::time::Duration;

/// Wait before restart n (0-based), seconds. Index is capped at the last entry.
pub const BACKOFF_SECS: [f64; 5] = [0.5, 1.0, 2.0, 4.0, 8.0];
/// The delay may be off by this fraction either way, so a crowd of shells does not restart in step.
pub const JITTER: f64 = 0.2;
/// A child whose health checks passed this long is healthy: the next failure starts the backoff from the beginning.
pub const HEALTHY_AFTER: Duration = Duration::from_secs(60);
/// Crashes (exits and failed spawns) allowed inside `BREAKER_WINDOW`; the 5th trips the breaker.
pub const BREAKER_STARTS: usize = 5;
pub const BREAKER_WINDOW: Duration = Duration::from_secs(60);
/// Health checks in a row that may fail before the child counts as hung.
pub const HUNG_AFTER: u32 = 3;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum State {
    Starting,
    Running,
    Backoff,
    Tripped,
    Stopping,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Event {
    /// The child process exists (after spawn).
    Started,
    /// The child process ended, whatever the reason.
    Exited,
    /// A spawn attempt failed before the child was usable.
    SpawnFailed,
    /// The timer armed for `SpawnAfter` fired.
    SpawnDue,
    HealthOk,
    HealthFailed,
    /// The person quit (tray, `--quit`): never restart again.
    Quit,
    /// "Restart backend" after a trip.
    RestartRequested,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Action {
    Nothing,
    /// Arm a timer for this long, then send `SpawnDue`.
    SpawnAfter(Duration),
    /// Spawn now.
    Spawn,
    /// The child is hung: end it. Its exit comes back as `Exited`.
    KillChild,
    /// Too many crashes: no more spawns; show the error in tray and window.
    ShowTripped,
}

pub struct Supervisor<J: FnMut() -> f64> {
    state: State,
    jitter: J,
    crashes: Vec<Duration>,
    healthy_since: Option<Duration>,
    failures: usize,
    health_fails: u32,
    killing: bool,
}

impl<J: FnMut() -> f64> Supervisor<J> {
    /// `jitter` returns a number in [-1, 1]; it is scaled by `JITTER`. NaN counts as 0.
    pub fn new(jitter: J) -> Self {
        Supervisor {
            state: State::Starting,
            jitter,
            crashes: Vec::new(),
            healthy_since: None,
            failures: 0,
            health_fails: 0,
            killing: false,
        }
    }

    pub fn state(&self) -> State {
        self.state
    }

    fn backoff(&mut self) -> Duration {
        let base = BACKOFF_SECS[self.failures.min(BACKOFF_SECS.len() - 1)];
        let r = (self.jitter)();
        let r = if r.is_finite() { r.clamp(-1.0, 1.0) } else { 0.0 };
        Duration::from_secs_f64(base * (1.0 + r * JITTER))
    }

    /// A child died or a spawn failed: count it, then back off or trip.
    fn crash(&mut self, now: Duration) -> Action {
        if self.healthy_since.is_some_and(|s| now.saturating_sub(s) >= HEALTHY_AFTER) {
            self.failures = 0;
        }
        self.healthy_since = None;
        self.killing = false;
        self.health_fails = 0;
        self.crashes.push(now);
        self.crashes.retain(|t| now.saturating_sub(*t) < BREAKER_WINDOW);
        if self.crashes.len() >= BREAKER_STARTS {
            self.state = State::Tripped;
            return Action::ShowTripped;
        }
        let wait = self.backoff();
        self.failures += 1;
        self.state = State::Backoff;
        Action::SpawnAfter(wait)
    }

    pub fn on(&mut self, ev: Event, now: Duration) -> Action {
        use State::*;
        match (self.state, ev) {
            (_, Event::Quit) => {
                self.state = Stopping;
                Action::Nothing
            }
            (Stopping, _) => Action::Nothing,
            (Starting | Backoff, Event::SpawnDue) => Action::Spawn,
            (Starting | Backoff, Event::Started) => {
                self.state = Running;
                self.healthy_since = None;
                self.health_fails = 0;
                self.killing = false;
                Action::Nothing
            }
            (Running, Event::HealthOk) => {
                self.health_fails = 0;
                self.healthy_since.get_or_insert(now);
                Action::Nothing
            }
            (Running, Event::HealthFailed) => {
                self.healthy_since = None;
                if self.killing {
                    return Action::Nothing;
                }
                self.health_fails += 1;
                if self.health_fails >= HUNG_AFTER {
                    self.health_fails = 0;
                    self.killing = true;
                    Action::KillChild
                } else {
                    Action::Nothing
                }
            }
            (Starting | Running, Event::Exited) => self.crash(now),
            (Starting | Backoff, Event::SpawnFailed) => self.crash(now),
            (Tripped, Event::RestartRequested) => {
                self.crashes.clear();
                self.failures = 0;
                self.state = Starting;
                Action::SpawnAfter(Duration::ZERO)
            }
            _ => Action::Nothing,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(n: u64) -> Duration {
        Duration::from_secs(n)
    }

    fn sup() -> Supervisor<impl FnMut() -> f64> {
        Supervisor::new(|| 0.0)
    }

    fn wait_of(a: Action) -> f64 {
        match a {
            Action::SpawnAfter(d) => d.as_secs_f64(),
            other => panic!("expected a spawn, got {other:?}"),
        }
    }

    #[test]
    fn backoff_sequence() {
        let mut sv = sup();
        let mut now = 0;
        let mut got = Vec::new();
        for _ in 0..4 {
            sv.on(Event::Started, s(now));
            now += 20; // each run lasts 20 s: not healthy, and 4 starts stay under the breaker
            got.push(wait_of(sv.on(Event::Exited, s(now))));
            now += 20;
        }
        assert_eq!(got, vec![0.5, 1.0, 2.0, 4.0]);
    }

    #[test]
    fn backoff_stops_growing_at_eight_seconds() {
        let mut sv = sup();
        sv.failures = 9;
        assert_eq!(sv.backoff().as_secs_f64(), 8.0);
    }

    #[test]
    fn jitter_stays_within_twenty_percent() {
        let mut lo = Supervisor::new(|| -1.0);
        let mut hi = Supervisor::new(|| 1.0);
        assert!((lo.backoff().as_secs_f64() - 0.4).abs() < 1e-9);
        assert!((hi.backoff().as_secs_f64() - 0.6).abs() < 1e-9);
        let mut wild = Supervisor::new(|| 50.0);
        assert!(wild.backoff().as_secs_f64() <= 0.6 + 1e-9, "the jitter source is clamped");
    }

    #[test]
    fn a_healthy_minute_resets_the_backoff() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        assert_eq!(wait_of(sv.on(Event::Exited, s(10))), 0.5);
        sv.on(Event::Started, s(11));
        assert_eq!(wait_of(sv.on(Event::Exited, s(30))), 1.0);
        sv.on(Event::Started, s(31));
        sv.on(Event::HealthOk, s(32));
        assert_eq!(wait_of(sv.on(Event::Exited, s(100))), 0.5, "healthy for 68 s, so the next failure starts over");
    }

    #[test]
    fn breaker_trips_at_five_in_sixty() {
        let mut sv = sup();
        for i in 0..4 {
            sv.on(Event::Started, s(i * 5));
            assert!(matches!(sv.on(Event::Exited, s(i * 5 + 1)), Action::SpawnAfter(_)));
        }
        sv.on(Event::Started, s(20));
        assert_eq!(sv.on(Event::Exited, s(21)), Action::ShowTripped);
        assert_eq!(sv.state(), State::Tripped);
        assert_eq!(sv.on(Event::Exited, s(22)), Action::Nothing, "no more spawns while tripped");
    }

    #[test]
    fn five_starts_spread_over_more_than_a_minute_do_not_trip() {
        let mut sv = sup();
        for i in 0..8 {
            sv.on(Event::Started, s(i * 20));
            assert!(matches!(sv.on(Event::Exited, s(i * 20 + 1)), Action::SpawnAfter(_)), "start {i}");
        }
    }

    #[test]
    fn restart_after_trip_resets_everything() {
        let mut sv = sup();
        for i in 0..5 {
            sv.on(Event::Started, s(i));
            sv.on(Event::Exited, s(i));
        }
        assert_eq!(sv.state(), State::Tripped);
        assert_eq!(sv.on(Event::RestartRequested, s(30)), Action::SpawnAfter(Duration::ZERO));
        assert_eq!(sv.state(), State::Starting);
        sv.on(Event::Started, s(31));
        assert_eq!(wait_of(sv.on(Event::Exited, s(32))), 0.5);
    }

    #[test]
    fn restart_request_outside_a_trip_does_nothing() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        assert_eq!(sv.on(Event::RestartRequested, s(1)), Action::Nothing);
    }

    #[test]
    fn no_restart_when_stopping() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        sv.on(Event::Quit, s(5));
        assert_eq!(sv.state(), State::Stopping);
        assert_eq!(sv.on(Event::Exited, s(6)), Action::Nothing);
        assert_eq!(sv.on(Event::HealthFailed, s(7)), Action::Nothing);
        assert_eq!(sv.on(Event::RestartRequested, s(8)), Action::Nothing);
    }

    #[test]
    fn quit_while_tripped_or_in_backoff_stays_quiet() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        sv.on(Event::Exited, s(1));
        sv.on(Event::Quit, s(2));
        assert_eq!(sv.on(Event::Started, s(3)), Action::Nothing);
        assert_eq!(sv.state(), State::Stopping);
    }

    #[test]
    fn hung_detected_after_three_fails() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        assert_eq!(sv.on(Event::HealthFailed, s(10)), Action::Nothing);
        assert_eq!(sv.on(Event::HealthFailed, s(20)), Action::Nothing);
        assert_eq!(sv.on(Event::HealthFailed, s(30)), Action::KillChild);
    }

    #[test]
    fn one_good_answer_clears_the_count() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        sv.on(Event::HealthFailed, s(10));
        sv.on(Event::HealthFailed, s(20));
        sv.on(Event::HealthOk, s(30));
        assert_eq!(sv.on(Event::HealthFailed, s(40)), Action::Nothing);
        assert_eq!(sv.on(Event::HealthFailed, s(50)), Action::Nothing);
    }

    #[test]
    fn restart_after_exit() {
        let mut sv = sup();
        assert_eq!(sv.state(), State::Starting);
        sv.on(Event::Started, s(0));
        assert_eq!(sv.state(), State::Running);
        assert!(matches!(sv.on(Event::Exited, s(5)), Action::SpawnAfter(_)));
        assert_eq!(sv.state(), State::Backoff);
        sv.on(Event::Started, s(6));
        assert_eq!(sv.state(), State::Running);
    }

    #[test]
    fn running_without_passing_health_does_not_reset() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        assert_eq!(wait_of(sv.on(Event::Exited, s(10))), 0.5);
        sv.on(Event::Started, s(11));
        sv.on(Event::HealthFailed, s(40));
        assert_eq!(wait_of(sv.on(Event::Exited, s(100))), 1.0);
    }

    #[test]
    fn spawn_failing_every_time_trips() {
        let mut sv = sup();
        for i in 0..4 {
            assert!(matches!(sv.on(Event::SpawnFailed, s(i)), Action::SpawnAfter(_)), "fail {i}");
            assert_eq!(sv.on(Event::SpawnDue, s(i)), Action::Spawn);
        }
        assert_eq!(sv.on(Event::SpawnFailed, s(4)), Action::ShowTripped);
        assert_eq!(sv.state(), State::Tripped);
    }

    #[test]
    fn quit_cancels_a_pending_spawn() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        assert!(matches!(sv.on(Event::Exited, s(1)), Action::SpawnAfter(_)));
        sv.on(Event::Quit, s(2));
        assert_eq!(sv.on(Event::SpawnDue, s(3)), Action::Nothing);
    }

    #[test]
    fn spawn_due_while_running_or_tripped_does_nothing() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        assert_eq!(sv.on(Event::SpawnDue, s(1)), Action::Nothing);
        let mut t = sup();
        for i in 0..5 {
            t.on(Event::SpawnFailed, s(i));
        }
        assert_eq!(t.on(Event::SpawnDue, s(6)), Action::Nothing);
    }

    #[test]
    fn five_fast_kills_after_a_long_run_trip() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        let mut last = Action::Nothing;
        for i in 0..5 {
            let t = 300 + i * 5;
            last = sv.on(Event::Exited, s(t));
            if i < 4 {
                sv.on(Event::Started, s(t + 1));
            }
        }
        assert_eq!(last, Action::ShowTripped);
    }

    #[test]
    fn late_exit_during_backoff_is_ignored() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        sv.on(Event::Exited, s(1));
        assert_eq!(sv.on(Event::Exited, s(1)), Action::Nothing);
        assert_eq!(sv.on(Event::HealthFailed, s(2)), Action::Nothing);
    }

    #[test]
    fn kill_is_sent_once_until_exit() {
        let mut sv = sup();
        sv.on(Event::Started, s(0));
        for t in [10, 20, 30] {
            sv.on(Event::HealthFailed, s(t));
        }
        for t in [40, 50, 60, 70] {
            assert_eq!(sv.on(Event::HealthFailed, s(t)), Action::Nothing);
        }
        assert!(matches!(sv.on(Event::Exited, s(71)), Action::SpawnAfter(_)));
    }

    #[test]
    fn nan_jitter_does_not_panic() {
        let mut sv = Supervisor::new(|| f64::NAN);
        assert_eq!(sv.backoff().as_secs_f64(), 0.5);
    }

    #[test]
    fn a_crash_exactly_sixty_seconds_old_is_not_counted() {
        let mut sv = sup();
        for i in 0..4 {
            sv.on(Event::SpawnFailed, s(i * 20));
            sv.on(Event::SpawnDue, s(i * 20));
        }
        // crashes at 0,20,40,60: the one at 0 is 60 s old at t=60 and drops out
        assert!(matches!(sv.on(Event::SpawnFailed, s(60)), Action::SpawnAfter(_)));
    }
}
