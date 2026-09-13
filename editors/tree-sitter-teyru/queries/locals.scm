; Locals for Teyru: what opens a scope, what binds a name, and what reads one.
; Names follow nvim-treesitter (`@local.scope`, `@local.definition`,
; `@local.reference`), which Helix reads as well.

; ------------------------------------------------------------------ scopes

[
  (class_declaration)
  (interface_declaration)
  (annotation_type_declaration)
  (record_declaration)
  (enum_declaration)
  (method_declaration)
  (constructor_declaration)
  (compact_constructor_declaration)
  (annotation_method_declaration)
  (lambda_expression)
  (block)
  (block_that_may_yield)
  (switch_block)
  (catch_clause)
  (enhanced_for_statement)
  (for_statement)
  (try_statement)
  (synchronized_statement)
] @local.scope

; ------------------------------------------------------------ definitions

(class_declaration
  name: (identifier) @local.definition)

(interface_declaration
  name: (identifier) @local.definition)

(annotation_type_declaration
  name: (identifier) @local.definition)

(record_declaration
  name: (identifier) @local.definition)

(enum_declaration
  name: (identifier) @local.definition)

(method_declaration
  name: (identifier) @local.definition)

(annotation_method_declaration
  name: (identifier) @local.definition)

(constructor_declaration
  name: (identifier) @local.definition)

(compact_constructor_declaration
  name: (identifier) @local.definition)

(enum_constant
  name: (identifier) @local.definition)

(type_parameter
  name: (identifier) @local.definition)

(formal_parameter
  name: (identifier) @local.definition)

(setter_parameter
  name: (identifier) @local.definition)

(annotation_argument
  name: (identifier) @local.definition)

(catch_clause
  name: (identifier) @local.definition)

(enhanced_for_statement
  name: (identifier) @local.definition)

(resource
  declarator: (variable_declarator
    name: (identifier) @local.definition))

(local_variable_declaration
  declarator: (variable_declarator
    name: (identifier) @local.definition))

(field_declaration
  declarator: (variable_declarator
    name: (identifier) @local.definition))

(variable_declarator
  name: (identifier) @local.definition)

; a pattern binds the names it destructures into
(type_pattern
  name: (identifier) @local.definition)

(pattern
  name: (identifier) @local.definition)

(record_pattern
  body: (record_pattern_body
    (pattern
      name: (identifier) @local.definition)))

(inferred_parameters
  (identifier) @local.definition)

(lambda_expression
  parameters: (identifier) @local.definition)

(labeled_statement
  label: (identifier) @local.definition)

; -------------------------------------------------------------- references

(identifier) @local.reference

(field_access
  field: (identifier) @local.reference)

(method_invocation
  name: (identifier) @local.reference)
