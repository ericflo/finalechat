import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Sheet } from "./Common";
import { IconCheck, IconChevron, IconSearch } from "./Icons";
import { effectLabel, validateShape, type SettingEdit, type SettingsAction, type SettingsDescriptor, type SettingsProposal, type SettingsResource, type Shape, type Suggest, type Suggestion } from "../lib/artifacts";
import "./settings-form.css";

/** One thing a form asks about: a settings field, or an action's input. */
export interface FormItem {
  key: string;
  label: string;
  description?: string;
  schema: Shape;
  suggest?: Suggest;
  section?: string;
  group?: string;
  advanced?: boolean;
  writable: boolean;
  lockedReason?: string;
  /** It can go back to its default (unset, or left out). */
  resettable: boolean;
  /** When a change takes effect, if that is worth saying. */
  timing?: string;
}

/** Where an item's value stands. */
export interface ItemState {
  /** The value in force, or that will be: an edit, a saved or chosen value, or the default. */
  value: unknown;
  /** Set here (saved, or chosen) rather than left to its default. */
  set: boolean;
  /** Changed but not yet saved. */
  pending: boolean;
  /** What leaving it alone gives, when known. */
  fallback?: unknown;
  /** Where that comes from, when it is not simply the default. */
  fallbackFrom?: string;
}

/** A form over any settings source: the items, their state, and how to change them. */
export interface FormModel {
  items: FormItem[];
  lists: Record<string, Suggestion[]>;
  state: (key: string) => ItemState;
  /** Sets values, several at once (a model and the provider it comes from). */
  set: (changes: Record<string, unknown>) => void;
  /** Back to the default. */
  reset: (keys: string[]) => void;
  /** Drops unsaved changes, when changes are saved separately. */
  revert?: (keys: string[]) => void;
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
export const show = (v: unknown): string => (v === undefined || v === null || v === "" ? "—" : typeof v === "boolean" ? (v ? "On" : "Off") : typeof v === "string" ? v : JSON.stringify(v));
/** "Orchestrator" + "Reasoning effort" reads "Orchestrator reasoning effort". */
export const fullLabel = (i: { label: string; group?: string }) => (i.group ? `${i.group} ${/^[A-Z][a-z]/.test(i.label) ? i.label.charAt(0).toLowerCase() + i.label.slice(1) : i.label}` : i.label);

/** The suggestion for a value: in its group when grouped, else any. */
function suggestion(list: Suggestion[] | undefined, value: unknown, group?: unknown): Suggestion | undefined {
  if (!list || typeof value !== "string") return undefined;
  return list.find((s) => s.value === value && (typeof group !== "string" || !s.group || s.group === group)) ?? list.find((s) => s.value === value);
}

/** How a value reads: its suggestion's label when it has one. */
export function labelOf(model: FormModel, key: string, value: unknown): string {
  const item = model.items.find((i) => i.key === key);
  const group = item?.suggest?.group_by ? model.state(item.suggest.group_by).value : undefined;
  return suggestion(item?.suggest ? model.lists[item.suggest.list] : undefined, value, group)?.label || show(value);
}

const timingNote = (when: string) => (when === "next_turn" || when === "next_task" || when === "restart_required" ? `Takes effect: ${(effectLabel[when] ?? when).toLowerCase()}` : undefined);

/** A settings resource as a form. Edits collect into one settings.apply
 * proposal, kept against the version they began from. */
export function resourceModel(r: SettingsResource, proposal: SettingsProposal | null, onProposal: (p: SettingsProposal | null) => void): FormModel {
  const edits = proposal?.operation === "settings.apply" ? (proposal.edits ?? []) : [];
  const saved = (k: string) => Object.hasOwn(r.snapshot.saved, k);
  const field = (k: string) => r.descriptor.fields.find((f) => f.key === k);
  const commit = (next: SettingEdit[]) =>
    onProposal(next.length ? { operation: "settings.apply", schema_version: proposal?.schema_version || r.descriptor.schema_version, expected_version: proposal?.expected_version || r.snapshot.version, generation: proposal?.generation ?? r.generation, edits: next } : null);
  const without = (keys: string[]) => edits.filter((e) => !keys.includes(e.key));
  return {
    items: r.descriptor.fields.map((f) => ({ key: f.key, label: f.label, description: f.description, schema: f.schema, suggest: f.suggest, section: f.section, group: f.group, advanced: f.advanced, writable: f.writable, lockedReason: f.locked_reason, resettable: f.unset, timing: timingNote(f.effective_when) })),
    lists: r.descriptor.suggestions ?? {},
    state: (k) => {
      const e = edits.find((x) => x.key === k);
      const fallback = saved(k) ? undefined : r.snapshot.effective[k];
      const source = field(k)?.source;
      const fallbackFrom = !saved(k) && source && !/^(default|runtime:)/.test(source) ? source.replace(/^preset:/, "preset ").replace(/^env:/, "environment ") : undefined;
      if (e) return { value: e.op === "set" ? e.value : fallback, set: e.op === "set", pending: true, fallback, fallbackFrom };
      return { value: saved(k) ? r.snapshot.saved[k] : r.snapshot.effective[k], set: saved(k), pending: false, fallback, fallbackFrom };
    },
    set: (changes) => {
      let next = without(Object.keys(changes));
      for (const [k, v] of Object.entries(changes)) {
        const base = saved(k) ? r.snapshot.saved[k] : r.snapshot.effective[k];
        if (!same(v, base) || (!saved(k) && base === undefined)) next = [...next, { op: "set", key: k, value: v }];
      }
      commit(next);
    },
    reset: (keys) => commit([...without(keys), ...keys.filter((k) => saved(k) && field(k)?.unset).map((k): SettingEdit => ({ op: "unset", key: k }))]),
    revert: (keys) => commit(without(keys)),
  };
}

/** An action's inputs as a form: each left alone takes the value another
 * choice implies for it, or its declared default. */
export function startModel(descriptor: SettingsDescriptor, action: SettingsAction, values: Record<string, unknown>, onChange: (next: Record<string, unknown>) => void): FormModel {
  const inputs = action.inputs ?? [];
  const lists = descriptor.suggestions ?? {};
  const items: FormItem[] = inputs.flatMap((i) => {
    const schema = action.parameters.properties?.[i.name];
    return schema ? [{ key: i.name, label: i.label, description: i.description, schema, suggest: i.suggest, group: i.group, writable: true, resettable: true }] : [];
  });
  const usable = (k: string, v: unknown) => v !== undefined && items.some((i) => i.key === k && !validateShape(i.schema, v));
  const fallbackOf = (k: string, vals: Record<string, unknown>): { value: unknown; from?: string } | undefined => {
    const implied = (chosen: boolean) => {
      for (const i of inputs) {
        if (i.name === k || !i.suggest) continue;
        const v = chosen ? (usable(i.name, vals[i.name]) ? vals[i.name] : undefined) : usable(i.name, vals[i.name]) ? undefined : i.default;
        const s = v === undefined ? undefined : suggestion(lists[i.suggest.list], v);
        if (s?.implies && Object.hasOwn(s.implies, k)) return { value: s.implies[k], from: chosen ? s.label || String(v) : undefined };
      }
      return undefined;
    };
    const own = inputs.find((i) => i.name === k)?.default;
    return implied(true) ?? (own !== undefined ? { value: own } : undefined) ?? implied(false);
  };
  return {
    items,
    lists,
    state: (k) => {
      const fb = fallbackOf(k, values);
      return usable(k, values[k]) ? { value: values[k], set: true, pending: false, fallback: fb?.value, fallbackFrom: fb?.from } : { value: fb?.value, set: false, pending: false, fallback: fb?.value, fallbackFrom: fb?.from };
    },
    set: (changes) => {
      const next: Record<string, unknown> = { ...values, ...changes };
      // Choosing what it would be anyway is leaving it alone.
      for (const k of Object.keys(changes)) if (fallbackOf(k, next) !== undefined && same(fallbackOf(k, next)!.value, next[k])) delete next[k];
      onChange(next);
    },
    reset: (keys) => {
      const next = { ...values };
      for (const k of keys) delete next[k];
      onChange(next);
    },
  };
}

/** The parameters an action is sent with: each usable choice, and with a
 * choice made from a group (a provider's model), the group it came from. */
export function startParameters(model: FormModel): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const i of model.items) if (model.state(i.key).set) out[i.key] = model.state(i.key).value;
  for (const i of model.items) {
    const by = i.suggest?.group_by;
    if (by && out[i.key] !== undefined && out[by] === undefined && model.state(by).value !== undefined) out[by] = model.state(by).value;
  }
  return out;
}

/** One line per group (or ungrouped item): what it comes to, and whether it was changed. */
export function formSummary(model: FormModel): { title: string; text: string; changed: boolean }[] {
  return blocksOf(model.items).map((b) => {
    const targets = new Set(b.items.flatMap((i) => (i.suggest?.group_by ? [i.suggest.group_by] : [])));
    const parts = b.items.filter((i) => !targets.has(i.key) && !i.advanced).map((i) => labelOf(model, i.key, model.state(i.key).value)).filter((t) => t !== "—");
    return { title: b.group || b.first.label, text: parts.join(" · "), changed: b.items.some((i) => model.state(i.key).set || model.state(i.key).pending) };
  });
}

function blocksOf(items: FormItem[]): { group: string; section: string; first: FormItem; items: FormItem[] }[] {
  const out: { group: string; section: string; first: FormItem; items: FormItem[] }[] = [];
  for (const i of items) {
    const b = i.group ? out.find((x) => x.group === i.group && x.section === (i.section || "")) : undefined;
    if (b) b.items.push(i);
    else out.push({ group: i.group || "", section: i.section || "", first: i, items: [i] });
  }
  return out;
}

const matches = (i: FormItem, q: string) => `${i.group ?? ""} ${i.label} ${i.key} ${i.description ?? ""}`.toLowerCase().includes(q);

/** A settings form: sections as tabs, groups as cards, rarely changed
 * settings on request, and a way back to the default on everything that has one. */
export function SettingsForm({ model, empty }: { model: FormModel; empty?: ReactNode }) {
  const root = useRef<HTMLDivElement>(null);
  const sections = useMemo(() => [...new Set(model.items.map((i) => i.section || ""))], [model.items]);
  const [tab, setTab] = useState("");
  const current = sections.includes(tab) ? tab : (sections[0] ?? "");
  const [query, setQuery] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const q = query.trim().toLowerCase();
  const inView = model.items.filter((i) => (q ? matches(i, q) : (i.section || "") === current));
  // Advanced settings stay out of the way unless asked for, or changed.
  const shown = inView.filter((i) => q || advanced || !i.advanced || model.state(i.key).set || model.state(i.key).pending);
  const hidden = inView.length - shown.length;
  if (!model.items.length) return <div className="sf">{empty ?? <p className="sf-none">No settings are published here.</p>}</div>;
  return (
    <div className="sf" ref={root}>
      {sections.length > 1 && !q && (
        <nav className="sf-tabs" aria-label="Settings sections">
          {sections.map((s) => (
            <button
              key={s}
              type="button"
              aria-pressed={s === current}
              onClick={() => {
                setTab(s);
                root.current?.scrollIntoView({ block: "start" });
              }}
            >
              {s || "Other"}
              {model.items.some((i) => (i.section || "") === s && model.state(i.key).pending) && <i className="sf-dot" aria-label="(unsaved changes)" />}
            </button>
          ))}
        </nav>
      )}
      {model.items.length > 12 && (
        <label className="sf-search">
          <IconSearch aria-hidden="true" />
          <input type="search" aria-label="Find a setting" placeholder="Find a setting" value={query} onChange={(e) => setQuery(e.target.value)} autoCapitalize="none" autoCorrect="off" spellCheck={false} />
        </label>
      )}
      {blocksOf(shown).map((b) => (b.group ? <GroupCard key={`${b.section}|${b.group}`} model={model} title={b.group} items={b.items} /> : <SingleCard key={b.first.key} model={model} item={b.first} />))}
      {q && !shown.length && <p className="sf-none">No setting matches “{query.trim()}”.</p>}
      {!q && (hidden > 0 || advanced) && inView.some((i) => i.advanced) && (
        <button type="button" className="sf-more" onClick={() => setAdvanced((v) => !v)}>
          {advanced ? "Hide advanced settings" : `Show ${hidden} advanced setting${hidden === 1 ? "" : "s"}`}
        </button>
      )}
    </div>
  );
}

function Status({ pending, set, resettable, from }: { pending: boolean; set: boolean; resettable: boolean; from?: string }) {
  if (pending) return <span className="sf-badge unsaved">Unsaved</span>;
  if (!resettable) return null;
  if (set) return <span className="sf-badge custom">Changed</span>;
  return <span className="sf-badge">{from ? `From ${from}` : "Default"}</span>;
}

function CardActions({ model, items }: { model: FormModel; items: FormItem[] }) {
  const writable = items.filter((i) => i.writable);
  const pending = writable.filter((i) => model.state(i.key).pending);
  const set = writable.filter((i) => i.resettable && model.state(i.key).set);
  if (pending.length && model.revert) return <button type="button" className="sf-link" onClick={() => model.revert!(pending.map((i) => i.key))}>Undo</button>;
  if (set.length) return <button type="button" className="sf-link" onClick={() => model.reset(set.map((i) => i.key))}>Use default</button>;
  return null;
}

function GroupCard({ model, title, items }: { model: FormModel; title: string; items: FormItem[] }) {
  // A field others group their suggestions by (a provider) is chosen with them.
  const targets = new Set(items.flatMap((i) => (i.suggest?.group_by ? [i.suggest.group_by] : [])));
  const members = items.filter((i) => !targets.has(i.key));
  const states = items.map((i) => model.state(i.key));
  const set = items.some((i, n) => i.resettable && !!states[n]?.set);
  const from = states.find((s) => !s.set && s.fallbackFrom)?.fallbackFrom;
  return (
    <section className={`sf-card${states.some((s) => s.pending) ? " pending" : ""}`} aria-label={title}>
      <header>
        <h4>{title}</h4>
        <Status pending={states.some((s) => s.pending)} set={set} resettable={items.some((i) => i.resettable)} from={from} />
        <CardActions model={model} items={items} />
      </header>
      {members.map((i) => (
        <Row key={i.key} model={model} item={i} inCard own={members.filter((m) => (m.resettable && model.state(m.key).set) || model.state(m.key).pending).length > 1} />
      ))}
    </section>
  );
}

function SingleCard({ model, item }: { model: FormModel; item: FormItem }) {
  const st = model.state(item.key);
  return (
    <section className={`sf-card single${st.pending ? " pending" : ""}`}>
      <Row model={model} item={item} status={<Status pending={st.pending} set={st.set} resettable={item.resettable} from={st.fallbackFrom} />} actions={<CardActions model={model} items={[item]} />} />
    </section>
  );
}

function Row({ model, item, inCard, own: ownLinks, status, actions }: { model: FormModel; item: FormItem; inCard?: boolean; own?: boolean; status?: ReactNode; actions?: ReactNode }) {
  const st = model.state(item.key);
  const id = `sf-${item.key.replace(/[^\w-]/g, "_")}`;
  // Inside a card, each setting can still go back on its own.
  const own = inCard && ownLinks && item.writable ? (st.pending && model.revert ? <button type="button" className="sf-link small" onClick={() => model.revert!([item.key])}>Undo</button> : item.resettable && st.set ? <button type="button" className="sf-link small" onClick={() => model.reset([item.key])}>Use default</button> : null) : null;
  const note = [item.description, !item.writable ? item.lockedReason || "Read only" : item.timing].filter(Boolean).join(" · ");
  return (
    <div className={`sf-row${inCard ? " in-card" : ""}`}>
      <div className="sf-row-head">
        <label id={`${id}-label`} htmlFor={id}>
          {inCard ? item.label : fullLabel(item)}
        </label>
        {status}
        {own}
        {actions}
      </div>
      <Control model={model} item={item} id={id} />
      {note && <small className="sf-note">{note}</small>}
    </div>
  );
}

function Control({ model, item, id }: { model: FormModel; item: FormItem; id: string }) {
  const s = item.schema;
  const list = item.suggest ? model.lists[item.suggest.list] : undefined;
  if (s.enum) {
    if (list?.length && s.enum.length <= 8) return <ChoiceList model={model} item={item} id={id} list={list} />;
    if (list?.length) return <PickerButton model={model} item={item} id={id} list={list} />;
    if (s.enum.length <= 8 && s.enum.every((o) => show(o).length <= 14)) return <Segmented model={model} item={item} id={id} options={s.enum} />;
    return <Select model={model} item={item} id={id} />;
  }
  if (s.type === "boolean") return <Segmented model={model} item={item} id={id} options={[true, false]} />;
  if (s.type === "string" && list) return <PickerButton model={model} item={item} id={id} list={list} />;
  if (s.type === "array" || s.type === "object") return <JsonInput model={model} item={item} id={id} />;
  return <TextInput model={model} item={item} id={id} />;
}

/** Picks a value; picking what it would be anyway goes back to the default. */
function choose(model: FormModel, item: FormItem, v: unknown) {
  const st = model.state(item.key);
  if (item.resettable && st.set && st.fallback !== undefined && same(v, st.fallback)) model.reset([item.key]);
  else model.set({ [item.key]: v });
}

function Segmented({ model, item, id, options }: { model: FormModel; item: FormItem; id: string; options: unknown[] }) {
  const st = model.state(item.key);
  // When what the default comes to is unknown, it is a choice of its own.
  const unknownDefault = item.resettable && st.fallback === undefined;
  return (
    <div className="sf-seg" role="radiogroup" id={id} aria-labelledby={`${id}-label`}>
      {unknownDefault && (
        <button type="button" role="radio" aria-checked={!st.set} className={!st.set ? "on inherited" : ""} disabled={!item.writable} onClick={() => model.reset([item.key])}>
          Default
        </button>
      )}
      {options.map((o, n) => {
        const on = (st.set || !unknownDefault) && same(o, st.value);
        const isDefault = item.resettable && st.fallback !== undefined && same(o, st.fallback);
        return (
          <button key={n} type="button" role="radio" aria-checked={on} className={`${on ? "on" : ""}${on && !st.set && !st.pending ? " inherited" : ""}`} disabled={!item.writable} title={isDefault ? "The default" : undefined} onClick={() => choose(model, item, o)}>
            {show(o)}
            {isDefault && st.set && <i className="sf-default" aria-label="(default)" />}
          </button>
        );
      })}
    </div>
  );
}

function ChoiceList({ model, item, id, list }: { model: FormModel; item: FormItem; id: string; list: Suggestion[] }) {
  const st = model.state(item.key);
  const fallback = st.fallback !== undefined ? suggestion(list, st.fallback) : undefined;
  const rows = (item.schema.enum ?? []).map((v) => ({ v, s: suggestion(list, v) }));
  return (
    <div className="sf-choices" role="radiogroup" id={id} aria-labelledby={`${id}-label`}>
      {item.resettable && (
        <Choice on={!st.set} disabled={!item.writable} title="Default" detail={st.fallback !== undefined ? [fallback?.label || show(st.fallback), fallback?.detail].filter(Boolean).join(" — ") : undefined} onClick={() => model.reset([item.key])} />
      )}
      {rows.map(({ v, s }) => (
        <Choice key={JSON.stringify(v)} on={(st.set || !item.resettable) && same(st.value, v)} disabled={!item.writable} title={s?.label || (v === "" ? "None" : show(v))} detail={s?.detail} onClick={() => model.set({ [item.key]: v })} />
      ))}
    </div>
  );
}

function Choice({ on, title, detail, disabled, onClick }: { on: boolean; title: string; detail?: string; disabled: boolean; onClick: () => void }) {
  return (
    <button type="button" role="radio" aria-checked={on} className={`sf-choice${on ? " on" : ""}`} disabled={disabled} onClick={onClick}>
      <span className="sf-radio" aria-hidden="true" />
      <span className="sf-choice-text">
        <span>{title}</span>
        {detail && <small>{detail}</small>}
      </span>
    </button>
  );
}

function Select({ model, item, id }: { model: FormModel; item: FormItem; id: string }) {
  const st = model.state(item.key);
  const name = (v: unknown) => (v === "" ? "None" : show(v));
  const value = st.set || st.pending || !item.resettable ? (JSON.stringify(st.value) ?? "") : "";
  return (
    <select id={id} className="sf-select" disabled={!item.writable} value={value} onChange={(e) => (e.target.value === "" ? model.reset([item.key]) : model.set({ [item.key]: JSON.parse(e.target.value) }))}>
      {item.resettable ? <option value="">{st.fallback !== undefined ? `Default (${name(st.fallback)})` : "Default"}</option> : value === "" && <option value="" disabled>Choose…</option>}
      {(item.schema.enum ?? []).map((v) => (
        <option key={JSON.stringify(v)} value={JSON.stringify(v)}>
          {name(v)}
        </option>
      ))}
    </select>
  );
}

function TextInput({ model, item, id }: { model: FormModel; item: FormItem; id: string }) {
  const st = model.state(item.key);
  const numeric = item.schema.type === "integer" || item.schema.type === "number";
  // Left to its default, the field is empty and the default shows as the placeholder.
  const own = st.set || st.pending || !item.resettable ? st.value : undefined;
  const text = (v: unknown) => (v === undefined || v === null ? "" : String(v));
  const [value, setValue] = useState(text(own));
  const [error, setError] = useState("");
  const last = useRef(own);
  useEffect(() => {
    if (!same(last.current, own)) {
      last.current = own;
      setValue(text(own));
      setError("");
    }
  }, [own]);
  const change = (t: string) => {
    setValue(t);
    if (t.trim() === "" && item.resettable) {
      setError("");
      last.current = undefined;
      model.reset([item.key]);
      return;
    }
    const v = numeric ? (t.trim() === "" ? null : Number(t)) : t;
    setError(validateShape(item.schema, v) || "");
    last.current = v;
    model.set({ [item.key]: v });
  };
  const long = item.schema.type === "string" && (item.schema.max_length || 0) > 8192;
  const placeholder = st.fallback !== undefined && !st.set ? show(st.fallback) : item.resettable ? "Default" : "";
  return (
    <>
      {long ? (
        <textarea id={id} className="sf-input" rows={5} disabled={!item.writable} value={value} placeholder={placeholder} onChange={(e) => change(e.target.value)} />
      ) : (
        <input id={id} className="sf-input" type={numeric ? "number" : "text"} inputMode={numeric ? "numeric" : undefined} min={item.schema.minimum} max={item.schema.maximum} autoCapitalize="none" autoCorrect="off" spellCheck={false} disabled={!item.writable} value={value} placeholder={placeholder} onChange={(e) => change(e.target.value)} />
      )}
      {error && <p className="sf-error" role="alert">{error}</p>}
    </>
  );
}

function JsonInput({ model, item, id }: { model: FormModel; item: FormItem; id: string }) {
  const st = model.state(item.key);
  const [value, setValue] = useState(JSON.stringify(st.value, null, 2) ?? "");
  const [error, setError] = useState("");
  return (
    <>
      <textarea
        id={id}
        className="sf-input mono"
        rows={4}
        disabled={!item.writable}
        value={value}
        onChange={(e) => {
          setValue(e.target.value);
          try {
            const v: unknown = JSON.parse(e.target.value);
            setError(validateShape(item.schema, v) || "");
            model.set({ [item.key]: v });
          } catch {
            setError("Enter valid JSON.");
          }
        }}
      />
      {error && <p className="sf-error" role="alert">{error}</p>}
    </>
  );
}

/** A value picked from a long list (a model): shows its name and where it
 * comes from, and opens a searchable picker. */
function PickerButton({ model, item, id, list: all }: { model: FormModel; item: FormItem; id: string; list: Suggestion[] }) {
  const [open, setOpen] = useState(false);
  const st = model.state(item.key);
  // A list of fixed choices offers exactly those.
  const list = item.schema.enum ? item.schema.enum.flatMap((v) => (typeof v === "string" && v !== "" ? [suggestion(all, v) ?? { value: v }] : [])) : all;
  const by = item.suggest?.group_by ? model.items.find((i) => i.key === item.suggest!.group_by) : undefined;
  const group = by ? model.state(by.key).value : undefined;
  const hit = suggestion(list, st.value, group);
  const groupName = by && typeof group === "string" ? labelOf(model, by.key, group) : "";
  const main = hit?.label || show(st.value);
  const sub = [groupName, hit?.label && hit.label !== st.value ? show(st.value) : "", hit?.detail].filter(Boolean).join(" · ");
  return (
    <>
      <button id={id} type="button" className="sf-pick" disabled={!item.writable} aria-label={`${fullLabel(item)}: ${main}${groupName ? ` at ${groupName}` : ""}. Change`} onClick={() => setOpen(true)}>
        <span className="sf-pick-text">
          <span className="sf-pick-main">{main}</span>
          {sub && <span className="sf-pick-sub">{sub}</span>}
        </span>
        <IconChevron className="sf-pick-more" aria-hidden="true" />
      </button>
      {open && (
        <PickerSheet
          title={fullLabel(item)}
          list={list}
          current={typeof st.value === "string" && (st.set || st.pending || !item.resettable) ? st.value : ""}
          currentGroup={typeof group === "string" ? group : undefined}
          fallback={typeof st.fallback === "string" ? st.fallback : undefined}
          groups={by ? groupChoices(model, by.key, list) : listGroups(list)}
          allowCustom={!item.schema.enum}
          defaultRow={item.resettable && !by ? { on: !st.set && !st.pending, detail: st.fallback !== undefined ? [suggestion(list, st.fallback)?.label || show(st.fallback), suggestion(list, st.fallback)?.detail].filter(Boolean).join(" — ") : undefined, pick: () => { model.reset([item.key]); setOpen(false); } } : undefined}
          onPick={(value, g) => {
            const changes: Record<string, unknown> = { [item.key]: value };
            if (by && g !== undefined && g !== group && (!by.schema.enum || by.schema.enum.includes(g))) changes[by.key] = g;
            model.set(changes);
            setOpen(false);
          }}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}

/** The groups a list's values come in, as the list names them. */
function listGroups(list: Suggestion[]): { value: string; label: string }[] {
  return [...new Set(list.map((s) => s.group ?? ""))].filter(Boolean).map((g) => ({ value: g, label: g }));
}

/** The groups a list's values come in (providers), named by the field they
 * belong to, in its order, with the ones the list has values for. */
function groupChoices(model: FormModel, key: string, list: Suggestion[]): { value: string; label: string }[] {
  const item = model.items.find((i) => i.key === key);
  const values = [...((item?.schema.enum ?? []).filter((v) => typeof v === "string") as string[]), ...list.map((s) => s.group ?? "")].filter((v, n, all) => v !== "" && all.indexOf(v) === n);
  return values.map((v) => ({ value: v, label: labelOf(model, key, v) }));
}

function PickerSheet({ title, list, current, currentGroup, fallback, groups, allowCustom, defaultRow, onPick, onClose }: { title: string; list: Suggestion[]; current: string; currentGroup?: string; fallback?: string; groups: { value: string; label: string }[]; allowCustom: boolean; defaultRow?: { on: boolean; detail?: string; pick: () => void }; onPick: (value: string, group?: string) => void; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const s of list) m.set(s.group ?? "", (m.get(s.group ?? "") ?? 0) + 1);
    return m;
  }, [list]);
  const offered = groups.filter((g) => counts.has(g.value) || g.value === currentGroup);
  // Everything, with the current group first: a search finds a model at any provider.
  const [only, setOnly] = useState("");
  const q = query.trim().toLowerCase();
  const words = q.split(/\s+/).filter(Boolean);
  const groupName = (g?: string) => groups.find((x) => x.value === g)?.label ?? g ?? "";
  // Every word somewhere: "opus anthropic" finds Claude Opus at Anthropic.
  const found = list.filter((s) => {
    if (only && s.group !== only) return false;
    const text = `${s.value} ${s.label ?? ""} ${s.detail ?? ""} ${groupName(s.group)}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
  const shown = found.slice(0, 250);
  const target = only || currentGroup;
  const typed = query.trim();
  const custom = allowCustom && typed !== "" && !list.some((s) => s.value === typed && (!target || !s.group || s.group === target)) ? typed : "";
  const customRow = (
    <button type="button" className="sf-option custom" onClick={() => onPick(custom, target)}>
      <span className="sf-option-text">
        <span>Use “{custom}”</span>
        <small>{target ? `As typed, at ${groupName(target)}` : "As typed"}</small>
      </span>
    </button>
  );
  const order = (g: string) => (g === currentGroup ? -1 : groups.findIndex((x) => x.value === g) + 1 || groups.length + 1);
  const sections = only || q ? [{ group: only, items: shown }] : [...new Set(shown.map((s) => s.group ?? ""))].sort((a, b) => order(a) - order(b)).map((g) => ({ group: g, items: shown.filter((s) => (s.group ?? "") === g) }));
  // On a phone the keyboard would cover the list: start on the list there,
  // and in the search box where there is a real keyboard.
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    if (window.matchMedia?.("(pointer: coarse)").matches) el.querySelector<HTMLElement>(".sf-option.on, .sf-option")?.focus({ preventScroll: true });
    else el.querySelector<HTMLInputElement>("input")?.focus();
    el.querySelector<HTMLElement>(".sf-option.on")?.scrollIntoView({ block: "center" });
  }, []);
  return (
    <Sheet onClose={onClose} label={title} className="sf-picker-sheet">
      <div className="sf-picker" ref={root}>
        <h2>{title}</h2>
        <form
          className="sf-search"
          onSubmit={(e) => {
            e.preventDefault();
            if (found.length === 1 && !custom) onPick(found[0]!.value, found[0]!.group);
            else if (custom) onPick(custom, target);
          }}
        >
          <IconSearch aria-hidden="true" />
          <input type="search" aria-label={`Search ${title}`} placeholder={allowCustom ? "Search, or type any id" : "Search"} enterKeyHint="done" autoCapitalize="none" autoCorrect="off" spellCheck={false} value={query} onChange={(e) => setQuery(e.target.value)} />
        </form>
        {offered.length > 1 && (
          <div className="sf-filter" role="group" aria-label="Show">
            <button type="button" aria-pressed={!only} onClick={() => setOnly("")}>
              All <small>{list.length}</small>
            </button>
            {offered.map((g) => (
              <button key={g.value} type="button" aria-pressed={only === g.value} onClick={() => setOnly(g.value)}>
                {g.label} <small>{counts.get(g.value) ?? 0}</small>
              </button>
            ))}
          </div>
        )}
        <div className="sf-options">
          {defaultRow && !q && !only && (
            <button type="button" className={`sf-option${defaultRow.on ? " on" : ""}`} aria-current={defaultRow.on || undefined} onClick={defaultRow.pick}>
              <span className="sf-option-text">
                <span>Default</span>
                {defaultRow.detail && <small>{defaultRow.detail}</small>}
              </span>
              {defaultRow.on && <IconCheck className="sf-option-check" aria-hidden="true" />}
            </button>
          )}
          {custom && !found.length && customRow}
          {sections.map((sec) => (
            <div key={sec.group || "-"}>
              {!only && !q && sec.group && sections.length > 1 && <div className="sf-options-title">{groupName(sec.group)}</div>}
              {sec.items.map((s) => {
                const on = s.value === current && (!currentGroup || !s.group || s.group === currentGroup);
                return (
                  <button key={`${s.group}|${s.value}`} type="button" className={`sf-option${on ? " on" : ""}`} aria-current={on || undefined} onClick={() => onPick(s.value, s.group)}>
                    <span className="sf-option-text">
                      <span>
                        {s.label || s.value}
                        {s.value === fallback && !defaultRow && <em className="sf-badge">Default</em>}
                      </span>
                      <small>{[(only || q) && !only && s.group ? groupName(s.group) : "", s.label && s.label !== s.value ? s.value : "", s.detail].filter(Boolean).join(" · ")}</small>
                    </span>
                    {on && <IconCheck className="sf-option-check" aria-hidden="true" />}
                  </button>
                );
              })}
            </div>
          ))}
          {custom && found.length > 0 && customRow}
          {!found.length && !custom && <p className="sf-none">{list.length ? "Nothing matches." : "No suggestions yet."}{allowCustom ? " Type any id to use it." : ""}</p>}
          {found.length > shown.length && <p className="sf-none">Showing {shown.length} of {found.length}. Search to narrow.</p>}
        </div>
      </div>
    </Sheet>
  );
}
