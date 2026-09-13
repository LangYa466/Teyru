; Indentation for Teyru.
;
; Teyru ends statements at a line break instead of a semicolon, so there is
; nothing here for `;`: the nodes that indent are the bracketed ones, exactly
; as in Java, plus the `for` header (whose three parts are separated by
; colons) and the accessor block of a property.

[
  (block)
  (block_that_may_yield)
  (class_body)
  (record_body)
  (enum_body)
  (annotation_type_body)
  (switch_block)
  (array_initializer)
  (element_value_array_initializer)
  (argument_list)
  (annotation_argument_list)
  (formal_parameters)
  (parenthesized_expression)
  (resource_specification)
  (type_arguments)
  (type_parameters)
  (accessor_block)
] @indent.begin

[
  "}"
  ")"
  "]"
  ">"
] @indent.end @indent.branch

; a for header that is spread over several lines indents after the `for`
(for_statement
  "(" @indent.begin)

(enhanced_for_statement
  "(" @indent.begin)

[
  (line_comment)
  (block_comment)
] @indent.auto

; a case that falls through carries its body on the following lines
(switch_rule
  ":" @indent.branch)
