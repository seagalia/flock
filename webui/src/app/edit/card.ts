import { Component, computed, input, model, output, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { clone, DescMessage, Message } from '@bufbuild/protobuf';
import { policyNamesOf } from '../forms/external-auth-picker';
import { AgentKinds, FieldError, ProtoForm } from '../forms/proto-form';
import { CardDef } from './cards';

// What a card's Save came to. A card that is done leaves its edit mode, otherwise it stays with the errors.
export type SaveOutcome = { done: true } | { done: false; errors: FieldError[]; problem: string };

// EditCard shows one section of a resource read-only, with its own Edit and Save. In create mode it is a form of
// the whole draft resource from the start, and the panel's Create sends it.
@Component({
  selector: 'flock-edit-card',
  imports: [MatButtonModule, MatIconModule, MatTooltipModule, ProtoForm],
  template: `
    <section class="card" [attr.data-card]="def().title">
      <header>
        <h3>{{ def().title }}</h3>
        @if (!creating()) {
          @if (draft(); as current) {
            <button mat-button type="button" class="cancel" (click)="cancel()">Cancel</button>
            <button
              mat-flat-button
              type="button"
              class="save"
              [disabled]="busy()"
              (click)="onSave(current)"
            >
              Save
            </button>
          } @else {
            <button
              mat-stroked-button
              type="button"
              class="edit"
              [disabled]="!!editReason() || busy()"
              disabledInteractive
              [matTooltip]="editReason()"
              (click)="onEdit()"
            >
              <mat-icon>edit</mat-icon>
              Edit
            </button>
          }
        }
      </header>
      @if (denial()) {
        <p class="reason" role="status">{{ denial() }}</p>
      }
      @if (creating()) {
        <flock-proto-form
          [schema]="schema()"
          [(value)]="value"
          [fields]="createFields()"
          [readonly]="readonly()"
          [errors]="errors()"
          [agentKinds]="shownAgentKinds()"
          [policyNames]="policyNames()"
          [itemGreyed]="itemGreyed()"
        />
      } @else if (draft(); as current) {
        <flock-proto-form
          [schema]="schema()"
          [value]="current"
          (valueChange)="draft.set($event)"
          [fields]="def().fields"
          [readonly]="readonly()"
          [errors]="shownErrors()"
          [agentKinds]="shownAgentKinds()"
          [policyNames]="policyNames()"
          [itemGreyed]="itemGreyed()"
          (dirty)="dirty.emit($event)"
        />
      } @else {
        <flock-proto-form
          [schema]="schema()"
          [value]="value()"
          [fields]="def().fields"
          [allReadonly]="true"
          [agentKinds]="shownAgentKinds()"
          [policyNames]="policyNames()"
          [itemGreyed]="itemGreyed()"
        />
      }
      @if (shownAgentKinds()?.reason; as agentsReason) {
        <p class="reason" role="status">{{ agentsReason }}</p>
      }
      @if (problem()) {
        <p class="problem" role="alert">{{ problem() }}</p>
      }
    </section>
  `,
  styles: `
    .card {
      border: 1px solid var(--flock-border);
      border-radius: 8px;
      padding: 8px 16px 16px;
      margin-bottom: 16px;
      background: var(--mat-sys-surface-container-lowest);
    }
    header {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    h3 {
      flex: 1;
      margin: 8px 0 16px;
      font-weight: 500;
    }
    .reason {
      margin: -8px 0 16px;
      color: var(--mat-sys-on-surface-variant);
      text-align: right;
    }
    .problem {
      color: var(--mat-sys-error);
      margin: 8px 0 0;
    }
  `,
})
export class EditCard {
  readonly def = input.required<CardDef>();
  readonly schema = input.required<DescMessage>();
  // the stored resource in view mode, the draft of the whole resource in create mode
  readonly value = model.required<Message>();
  readonly creating = input(false);
  readonly readonly = input<string[]>([]);
  // why Edit is greyed before any click (the lock, an admin-only change), empty when it isn't
  readonly greyed = input('');
  readonly errors = input<FieldError[]>([]);
  readonly agentKinds = input<AgentKinds | null>(null);
  // reads the agents serving the resource, called once per edit session of an advanced card
  readonly loadAgentKinds = input<(() => Promise<AgentKinds>) | null>(null);
  readonly itemGreyed = input<Record<string, (item: Message) => string>>({});
  // the reason the user may not edit, null when the card may open its form
  readonly allowEdit = input<() => Promise<string | null>>(async () => null);
  readonly save = input<(card: CardDef, draft: Message) => Promise<SaveOutcome>>(async () => ({
    done: true,
  }));
  readonly dirty = output<boolean>();

  // the controller gives the UUID on create, a field nobody can fill would only take room
  protected readonly createFields = computed(() =>
    this.def().fields.filter((field) => field !== 'UUID'),
  );

  // the card's copy of the resource while its form is open, null in view mode
  protected readonly draft = signal<Message | null>(null);
  protected readonly denial = signal('');
  protected readonly busy = signal(false);
  protected readonly problem = signal('');
  private readonly saveErrors = signal<FieldError[]>([]);

  private readonly loadedKinds = signal<AgentKinds | null>(null);
  protected readonly shownAgentKinds = computed(() => this.loadedKinds() ?? this.agentKinds());

  // from the form as it stands, so a policy added and not saved yet can already be picked
  protected readonly policyNames = computed(() => {
    const edited = this.creating() ? this.value() : (this.draft() ?? this.value());
    return policyNamesOf(this.schema(), edited);
  });

  protected readonly editReason = computed(() => this.greyed() || this.denial());
  protected readonly shownErrors = computed(() => [...this.errors(), ...this.saveErrors()]);

  protected async onEdit() {
    if (this.editReason()) {
      return;
    }
    this.busy.set(true);
    this.problem.set('');
    try {
      const reason = await this.allowEdit()();
      if (reason) {
        this.denial.set(reason);
        return;
      }
      const load = this.def().advanced ? this.loadAgentKinds() : null;
      if (load) {
        // the agents decide what the card offers, a failed read leaves every kind open rather than blocking the edit
        this.loadedKinds.set(
          await load().catch(
            (): AgentKinds => ({ kinds: 'any', reason: 'The agents could not be read' }),
          ),
        );
      }
      this.draft.set(clone(this.schema(), this.value()));
    } catch (err) {
      this.problem.set(err instanceof Error ? err.message : String(err));
    } finally {
      this.busy.set(false);
    }
  }

  protected cancel() {
    this.close();
  }

  protected async onSave(current: Message) {
    this.busy.set(true);
    this.problem.set('');
    try {
      const outcome = await this.save()(this.def(), current);
      if (outcome.done) {
        this.close();
        return;
      }
      this.saveErrors.set(outcome.errors);
      this.problem.set(outcome.problem);
    } catch (err) {
      this.problem.set(err instanceof Error ? err.message : String(err));
    } finally {
      this.busy.set(false);
    }
  }

  private close() {
    this.draft.set(null);
    this.loadedKinds.set(null);
    this.saveErrors.set([]);
    this.problem.set('');
    this.dirty.emit(false);
  }
}
