// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { IAuthRegistry } from "./IAuthRegistry.sol";

/**
 * @title AuthRegistry
 * @notice Clean-room, demo-only policy registry. See {IAuthRegistry} for why
 *         this exists (self-contained, no private deps).
 *
 * @dev Policy ids start at 1; id 0 is reserved for "unset". Each policy has an
 *      admin (the only address allowed to modify membership), a type, an
 *      optional parent for inheritance, and a membership set.
 *
 *      Authorization with inheritance:
 *        - WHITELIST: authorized(a) = listed[a] OR (parent != 0 AND parent authorizes a)
 *                     — a grant anywhere up the chain is sufficient.
 *        - BLACKLIST: authorized(a) = !listed[a] AND (parent == 0 OR parent authorizes a)
 *                     — a block anywhere up the chain is disqualifying.
 *
 *      This mirrors the observable behaviour the vendored template relies on:
 *      mint recipients must be explicitly whitelisted; transfers default-allow
 *      unless the address is blacklisted; and address(0) is whitelisted so that
 *      ERC-20 mint (from 0) and burn (to 0) clear the transfer policy.
 */
contract AuthRegistry is IAuthRegistry {
    struct Policy {
        address admin;
        PolicyType policyType;
        uint64 parent;
        bool exists;
        mapping(address => bool) member;
    }

    // Ids 0 and 1 are reserved sentinels (0 = "unset", 1 = reserved). Real
    // policies start at 2 — the vendored 06_Verify script asserts a configured
    // policy id is > 1, so a freshly deployed chain's first policy must be >= 2.
    uint64 private _nextPolicyId = 2;
    mapping(uint64 => Policy) private _policies;

    // ─── Creation ────────────────────────────────────────────────────────────

    function createPolicy(address admin, PolicyType policyType) external returns (uint64) {
        return _createPolicy(admin, policyType, 0);
    }

    function createPolicy(address admin, PolicyType policyType, uint64 parent)
        external
        returns (uint64)
    {
        if (parent != 0 && !_policies[parent].exists) revert PolicyNotFound();
        return _createPolicy(admin, policyType, parent);
    }

    function _createPolicy(address admin, PolicyType policyType, uint64 parent)
        internal
        returns (uint64 policyId)
    {
        policyId = _nextPolicyId++;
        Policy storage p = _policies[policyId];
        p.admin = admin;
        p.policyType = policyType;
        p.parent = parent;
        p.exists = true;
        emit PolicyCreated(policyId, admin, policyType, parent);
    }

    // ─── Membership (admin only) ──────────────────────────────────────────────

    function modifyPolicyWhitelist(uint64 policyId, address account, bool allowed) external {
        _setMember(policyId, account, allowed);
    }

    function modifyPolicyBlacklist(uint64 policyId, address account, bool blocked) external {
        _setMember(policyId, account, blocked);
    }

    function _setMember(uint64 policyId, address account, bool value) internal {
        Policy storage p = _policies[policyId];
        if (!p.exists) revert PolicyNotFound();
        if (msg.sender != p.admin) revert Unauthorized();
        p.member[account] = value;
        emit PolicyMembershipChanged(policyId, account, value);
    }

    // ─── Authorization ────────────────────────────────────────────────────────

    function isAuthorized(uint64 policyId, address account) public view returns (bool) {
        Policy storage p = _policies[policyId];
        if (!p.exists) revert PolicyNotFound();

        if (p.policyType == PolicyType.WHITELIST) {
            if (p.member[account]) return true;
            if (p.parent != 0) return isAuthorized(p.parent, account);
            return false;
        }

        // BLACKLIST
        if (p.member[account]) return false;
        if (p.parent != 0) return isAuthorized(p.parent, account);
        return true;
    }

    // ─── Views (operator convenience) ─────────────────────────────────────────

    function policyAdmin(uint64 policyId) external view returns (address) {
        return _policies[policyId].admin;
    }

    function getPolicyType(uint64 policyId) external view returns (PolicyType) {
        return _policies[policyId].policyType;
    }
}
