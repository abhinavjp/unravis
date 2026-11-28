package blackbox

import "testing"

func TestConfFromImagePath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`"C:\Program Files\Redis\RedisService.exe" --service-run "C:\Program Files\Redis\redis.windows-service.conf"`, `C:\Program Files\Redis\redis.windows-service.conf`},
		{`C:\Redis\redis-server.exe --service-run C:\Redis\redis.conf`, `C:\Redis\redis.conf`},
		{`"C:\Program Files\Memurai\memurai.exe" --service-run "C:\Program Files\Memurai\memurai.conf" --x`, `C:\Program Files\Memurai\memurai.conf`},
		{`"C:\Program Files\Redis\redis-server.exe"`, ``},
		{`redis.conf`, ``},
		{`\server\share\redis.conf`, ``},
	} {
		if got := confFromImagePath(c.in); got != c.want {
			t.Errorf("confFromImagePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
