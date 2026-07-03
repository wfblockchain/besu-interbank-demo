// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/**
 * @title IAuthRegistry
 * @notice Clean-room, demo-only reconstruction of the policy-based access
 *         registry that Bridge's stablecoin template calls into.
 *
 *         The interface surface here is exactly what the vendored template
 *         contracts and deploy scripts use — recovered from their public
 *         call sites, NOT copied from the private `withbridge/auth-registry`
 *         repository. It exists so the demo is fully self-contained and safe
 *         to share: no private dependencies, no proprietary code.
 *
 *         Semantics implemented in {AuthRegistry}:
 *           - WHITELIST policy: an address is authorized iff it has been
 *             explicitly whitelisted (address(0) is whitelisted so ERC-20
 *             mint/burn, which touch the zero address, pass the check).
 *           - BLACKLIST policy: an address is authorized iff it has NOT been
 *             blacklisted (default-allow).
 *           - Policies may declare a parent for hierarchical inheritance.
 */
interface IAuthRegistry {
    enum PolicyType {
        WHITELIST,
        BLACKLIST
    }

    /// @dev Thrown when a caller that is not the policy admin tries to modify it.
    error Unauthorized();

    /// @dev Thrown when a policy id does not correspond to a created policy.
    error PolicyNotFound();

    event PolicyCreated(uint64 indexed policyId, address indexed admin, PolicyType policyType, uint64 parent);
    event PolicyMembershipChanged(uint64 indexed policyId, address indexed account, bool value);

    /// @notice Creates a root policy (no parent) administered by `admin`.
    function createPolicy(address admin, PolicyType policyType) external returns (uint64 policyId);

    /// @notice Creates a policy that inherits from `parent`.
    function createPolicy(address admin, PolicyType policyType, uint64 parent)
        external
        returns (uint64 policyId);

    /// @notice Returns whether `account` is authorized under `policyId`.
    function isAuthorized(uint64 policyId, address account) external view returns (bool);

    /// @notice Adds/removes `account` on a WHITELIST policy. Admin only.
    function modifyPolicyWhitelist(uint64 policyId, address account, bool allowed) external;

    /// @notice Adds/removes `account` on a BLACKLIST policy. Admin only.
    function modifyPolicyBlacklist(uint64 policyId, address account, bool blocked) external;
}
