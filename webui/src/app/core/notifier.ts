import { inject, Injectable, signal } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Code, ConnectError } from '@connectrpc/connect';
import { ErrorDialog } from '../ui/error-dialog';

// Progress counts the calls in flight, the shell shows a progress bar while there is one
@Injectable({ providedIn: 'root' })
export class Progress {
  private readonly inFlight = signal(0);
  readonly active = () => this.inFlight() > 0;

  start() {
    this.inFlight.update((count) => count + 1);
  }

  done() {
    this.inFlight.update((count) => count - 1);
  }
}

@Injectable({ providedIn: 'root' })
export class Notifier {
  private snackBar = inject(MatSnackBar);
  private dialog = inject(MatDialog);

  info(message: string) {
    this.snackBar.open(message, 'ok', {
      horizontalPosition: 'end',
      verticalPosition: 'top',
      duration: 2000,
    });
  }

  // success tells a change went through, method is the rpc name (CreateVS, DeleteBackend, ...)
  success(method: string) {
    const isChange = /^(Create|Update|Delete)/.test(method);
    if (!isChange) {
      return;
    }
    const spaced = method
      .replace('VS', 'VirtualServer')
      .replace('VIP', 'Vip')
      .replace(/([A-Z])/g, ' $1')
      .trim();
    this.info(`Successfully ${spaced}.`);
  }

  error(err: ConnectError) {
    this.dialog.open(ErrorDialog, {
      data: {
        title: `Error ${Code[err.code]} occurred (code: ${err.code})`,
        message: err.rawMessage,
      },
    });
  }
}
