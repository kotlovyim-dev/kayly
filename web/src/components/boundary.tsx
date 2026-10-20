import { Component, type ReactNode } from 'react';

/**
 * Keeps one broken screen from blanking the whole app: the frame stays, and the screen says what
 * failed and how to get back.
 */
export class Boundary extends Component<{ resetKey: string; children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  // Moving to another screen tries again.
  componentDidUpdate(prev: { resetKey: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div role="alert" className="flex flex-col items-start gap-2 p-6">
        <p className="text-body font-medium">This screen hit a bug: {this.state.error.message}</p>
        <p className="text-body text-muted-foreground">
          The server is not affected. Reload the page; if it happens again, please report it with the message above.
        </p>
      </div>
    );
  }
}
