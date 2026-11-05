package reload

import "context"

// Fanout reloads a primary target then runs After hooks (e.g. pool invalidate).
type Fanout struct {
	Primary Reloader
	After   []func()
}

// Reload implements Reloader.
func (f *Fanout) Reload(ctx context.Context) error {
	err := f.Primary.Reload(ctx)
	for _, fn := range f.After {
		if fn != nil {
			fn()
		}
	}
	return err
}
