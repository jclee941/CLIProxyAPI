import json
from collections.abc import Callable
from typing import NotRequired, TypeGuard, TypeVar, TypedDict, cast


class DocumentShapeError(ValueError):
    def __init__(self) -> None:
        super().__init__("invalid_document_structure")


class Binding(TypedDict):
    token_ref: str
    profile_guid: str
    expected_gaia_sha256: str
    auth_user: NotRequired[int]


class AuthRecord(TypedDict):
    id: str
    label: str
    token_ref: str
    disabled: NotRequired[bool]


class Original(TypedDict):
    record: NotRequired[AuthRecord]
    binding: Binding
    disabled: bool


class PluginConfig(TypedDict):
    maintenance_sources: dict[str, Binding]


PluginConfigs = TypedDict("PluginConfigs", {"gemini-web": PluginConfig})


class Plugins(TypedDict):
    configs: PluginConfigs


class Config(TypedDict):
    plugins: Plugins


class ContainerState(TypedDict):
    Pid: int


class Container(TypedDict):
    Image: str
    State: ContainerState


class NativeRecord(TypedDict):
    id: str
    disabled: bool


class NativeList(TypedDict):
    files: list[NativeRecord]


class Account(TypedDict):
    enabled: bool
    models: list[dict[str, str]]


class AccountList(TypedDict):
    accounts: list[Account]


class Identity(TypedDict):
    account_sha256: str
    auth_user: int


class Flow(TypedDict):
    state: str
    status: str
    models_ready: bool
    account_id: NotRequired[str]
    extension_id: NotRequired[str]
    manager_origin: NotRequired[str]
    expected_identity: NotRequired[Identity]


class Capture(TypedDict):
    existing_id: str
    label: str
    token: str
    account_sha256: str
    auth_user: int
    validated: bool
    consent: bool


class Bundle(TypedDict):
    version: int
    bundle_id: str
    consent: bool
    accounts: list[Capture]


class Receipt(TypedDict):
    account_id: str
    state: NotRequired[str]
    phase: str


Parsed = TypeVar("Parsed")


def narrow(value: object, predicate: Callable[[object], TypeGuard[Parsed]]) -> Parsed:
    if predicate(value):
        return value
    raise DocumentShapeError()


def decode(raw: str | bytes, predicate: Callable[[object], TypeGuard[Parsed]]) -> Parsed:
    return narrow(cast(object, json.loads(raw)), predicate)


def is_dictionary(value: object) -> TypeGuard[dict[object, object]]:
    return isinstance(value, dict)


def is_mapping(value: object) -> TypeGuard[dict[str, object]]:
    return is_dictionary(value) and all(isinstance(key, str) for key in value)


def is_sequence(value: object) -> TypeGuard[list[object]]:
    return isinstance(value, list)


def is_text(value: object) -> TypeGuard[str]:
    return isinstance(value, str)


def is_integer(value: object) -> TypeGuard[int]:
    return type(value) is int


def list_of(value: object, predicate: Callable[[object], TypeGuard[Parsed]]) -> TypeGuard[list[Parsed]]:
    return is_sequence(value) and all(predicate(item) for item in value)


def map_of(value: object, predicate: Callable[[object], TypeGuard[Parsed]]) -> TypeGuard[dict[str, Parsed]]:
    return is_mapping(value) and all(predicate(item) for item in value.values())


def is_binding(value: object) -> TypeGuard[Binding]:
    return is_mapping(value) and all(is_text(value.get(key)) for key in ("token_ref", "profile_guid", "expected_gaia_sha256")) and ("auth_user" not in value or is_integer(value["auth_user"]))


def is_auth_record(value: object) -> TypeGuard[AuthRecord]:
    return is_mapping(value) and all(is_text(value.get(key)) for key in ("id", "label", "token_ref")) and ("disabled" not in value or isinstance(value["disabled"], bool))


def is_original(value: object) -> TypeGuard[Original]:
    return is_mapping(value) and ("record" not in value or is_auth_record(value["record"])) and is_binding(value.get("binding")) and isinstance(value.get("disabled"), bool)


def is_originals(value: object) -> TypeGuard[dict[str, Original]]:
    return map_of(value, is_original)


def is_config(value: object) -> TypeGuard[Config]:
    if not is_mapping(value):
        return False
    plugins = value.get("plugins")
    if not is_mapping(plugins):
        return False
    configs = plugins.get("configs")
    if not is_mapping(configs):
        return False
    gemini = configs.get("gemini-web")
    return is_mapping(gemini) and map_of(gemini.get("maintenance_sources"), is_binding)


def is_container(value: object) -> TypeGuard[Container]:
    if not is_mapping(value) or not is_text(value.get("Image")):
        return False
    state = value.get("State")
    return is_mapping(state) and is_integer(state.get("Pid"))


def is_containers(value: object) -> TypeGuard[list[Container]]:
    return list_of(value, is_container)


def is_native_record(value: object) -> TypeGuard[NativeRecord]:
    return is_mapping(value) and is_text(value.get("id")) and isinstance(value.get("disabled"), bool)


def is_native_list(value: object) -> TypeGuard[NativeList]:
    return is_mapping(value) and list_of(value.get("files"), is_native_record)


def is_model(value: object) -> TypeGuard[dict[str, str]]:
    return map_of(value, is_text)


def is_account(value: object) -> TypeGuard[Account]:
    return is_mapping(value) and isinstance(value.get("enabled"), bool) and list_of(value.get("models"), is_model)


def is_account_list(value: object) -> TypeGuard[AccountList]:
    return is_mapping(value) and list_of(value.get("accounts"), is_account)


def is_identity(value: object) -> TypeGuard[Identity]:
    return is_mapping(value) and is_text(value.get("account_sha256")) and is_integer(value.get("auth_user"))


def is_flow(value: object) -> TypeGuard[Flow]:
    return is_mapping(value) and all(is_text(value.get(key)) for key in ("state", "status")) and isinstance(value.get("models_ready"), bool) and all(key not in value or is_text(value[key]) for key in ("account_id", "extension_id", "manager_origin")) and ("expected_identity" not in value or is_identity(value["expected_identity"]))


def is_capture(value: object) -> TypeGuard[Capture]:
    return is_mapping(value) and all(is_text(value.get(key)) for key in ("existing_id", "label", "token", "account_sha256")) and is_integer(value.get("auth_user")) and all(isinstance(value.get(key), bool) for key in ("validated", "consent"))


def is_bundle(value: object) -> TypeGuard[Bundle]:
    return is_mapping(value) and is_integer(value.get("version")) and is_text(value.get("bundle_id")) and isinstance(value.get("consent"), bool) and list_of(value.get("accounts"), is_capture)


def is_receipt(value: object) -> TypeGuard[Receipt]:
    return is_mapping(value) and all(is_text(value.get(key)) for key in ("account_id", "phase")) and ("state" not in value or is_text(value["state"]))
