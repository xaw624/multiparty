-- F2: nombre visible de la cuenta conectada (por ejemplo, el canal de
-- YouTube). La política de YouTube pide mostrar con qué canal se actúa.
ALTER TABLE cuentas ADD COLUMN nombre_externo TEXT;
