//! Supervisor state machine of the shell (BL-307): when to restart the Go owner the shell itself started, when to give up, when to
//! call it hung. Pure: time comes in as a `Duration` since some fixed start, jitter comes from a function, nothing sleeps. The Windows
//! code feeds it events (child exited, health answered or failed) and carries out the `Action` it returns.
//!
//! Applies only to a spawned child. A shell that attached to an owner somebody else started never builds one.

use std::time::Duration;

/// Wait before restart n (0-based), seconds. Index is capped at the last entry.
pub const BACKOFF_SECS: [f64; 5] = [0.5, 1.0, 2.0, 4.0, 8.0];
/// The delay may be off by this fraction either way, so a crowd of shells does not restart in step.
pub const JITTER: f64 = 0.2;
/// A child that ran this long is healthy: the next failure starts the backoff from the beginning.
pub const HEALTHY_AFTER: Duration = Duration::from_secs(60);
/// Starts allowed inside `BREAKER_WINDOW` before the supervisor gives up.
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
    /// Spawn after this delay.
    SpawnAfter(Duration),
    /// The child is hung: end it. Its exit comes back as `Exited`.
    KillChild,
    /// Too many starts: no more spawns; show the error in tray and window.
    ShowTripped,
}

pub struct Supervisor<J: FnMut() -> f64> {
    state: State,
    jitter: J,
    started: Vec<Duration>,
    running_since: Option<Duration>,
    failures: usize,
    health_fails: u32,
}

impl<J: FnMut() -> f64> Supervisor<J> {
    /// `jitter` returns a number in [-1, 1]; it is scaled by `JITTER`.
    pub fn new(jitter: J) -> Self {
        Supervisor { state: State::Starting, jitter, started: Vec::new(), running_since: None, failures: 0, health_fails: 0 }
    }

    pub fn state(&self) -> State {
        self.state
    }

    fn starts_in_window(&self, now: Duration) -> usize {
        self.started.iter().filter(|t| now.saturating_sub(**t) < BREAKER_WINDOW).count()
    }

    fn backoff(&mut self) -> Duration {
        let base = BACKOFF_SECS[self.failures.min(BACKOFF_SECS.len() - 1)];
        let j = (self.jitter)().clamp(-1.0, 1.0) * JITTER;
        Duration::from_secs_f64(base * (1.0 + j))
    }

    pub fn on(&mut self, ev: Event, now: Duration) -> Action {
        use State::*;
        match (self.state, ev) {
            (_, Event::Quit) => {
                self.state = Stopping;
                Action::Nothing
            }
            (Stopping, _) => Action::Nothing,
            (Starting | Backoff, Event::Started) => {
                self.state = Running;
                self.started.push(now);
                self.started.retain(|t| now.saturating_sub(*t) < BREAKER_WINDOW);
                self.running_since = Some(now);
                self.health_fails = 0;
                Action::Nothing
            }
            (Running, Event::HealthOk) => {
                self.health_fails = 0;
                Action::Nothing
            }
            (Running, Event::HealthFailed) => {
                self.health_fails += 1;
                if self.health_fails >= HUNG_AFTER {
                    self.health_fails = 0;
                    Action::KillChild
                } else {
                    Action::Nothing
                }
            }
            (Starting | Running, Event::Exited) => {
                if self.running_since.is_some_and(|s| now.saturating_sub(s) >= HEALTHY_AFTER) {
                    self.failures = 0;
                }
                self.running_since = None;
                if self.starts_in_window(now) >= BREAKER_STARTS {
                    self.state = Tripped;
                    return Action::ShowTripped;
                }
                let wait = self.backoff();
                self.failures += 1;
                self.state = Backoff;
                Action::SpawnAfter(wait)
            }
            (Tripped, Event::RestartRequested) => {
                self.started.clear();
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
        assert_eq!(wait_of(sv.on(Event::Exited, s(100))), 0.5, "it ran 69 s, so the next failure starts over");
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
}
